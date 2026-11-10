package taproot

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/265866/taproot/internal/api"
	"github.com/265866/taproot/internal/config"
	"github.com/265866/taproot/internal/taproot/access"
	sdkaccess "github.com/265866/taproot/sdk/access"
	"github.com/265866/taproot/sdk/cliproxy/auth"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
)

var setupCodeLine = regexp.MustCompile(`setup code ([A-Z0-9]{4}-[A-Z0-9]{4})\. .*open (\S+) from the device`)

// startFirstRun runs Setup on the files in dir and returns the server and
// the setup code it logged, or "" when it logged none.
func startFirstRun(t *testing.T, configPath string) (fixture, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	t.Setenv("MANAGEMENT_PASSWORD", "")
	if err := os.Unsetenv("MANAGEMENT_PASSWORD"); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadConfigOptional(configPath, false)
	if err != nil {
		t.Fatal(err)
	}
	hook := test.NewGlobal()
	defer log.StandardLogger().ReplaceHooks(make(log.LevelHooks))
	opts, running, err := setup(context.Background(), cfg, configPath)
	if err != nil {
		t.Fatalf("Setup on the first-run files: %v", err)
	}
	t.Cleanup(running.stop)
	code := ""
	for _, e := range hook.AllEntries() {
		if m := setupCodeLine.FindStringSubmatch(e.Message); m != nil {
			if code != "" {
				t.Fatalf("setup code logged twice: %q", e.Message)
			}
			code = m[1]
			if want := "http://server." + tailnet + ":8317/"; m[2] != want || e.Level != log.WarnLevel {
				t.Errorf("setup code line %q (level %s), want the dashboard URL %s at warning level", e.Message, e.Level, want)
			}
		}
	}
	auths := auth.NewManager(nil, nil, nil)
	srv := api.NewServer(cfg, auths, sdkaccess.NewManager(), configPath, opts...)
	return fixture{h: srv.Handler(), taprootYAML: ConfigPath(configPath), auths: auths}, code
}

// The whole first start: no config files, a setup code in the log, a claim
// from a tailnet device, and recovery after the admin entries are removed.
func TestFirstRunClaim(t *testing.T) {
	t.Setenv("TAPROOT_CONFIG", "")
	dir, err := os.MkdirTemp("", "taproot-firstrun")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	configPath := filepath.Join(dir, "config.yaml")
	if code := runHandleArgs(t, "--config", configPath); code != -1 {
		t.Fatalf("HandleArgs exited %d", code)
	}
	// The written access section is empty; point it at the fake tailscaled
	// and add an address, as an operator might.
	tpPath := filepath.Join(dir, "taproot.yaml")
	extra := "  tailscale-socket: " + filepath.ToSlash(fakeLocalAPI(t)) + "\n  addresses:\n    - cidr: " + lanIP + "/32\nbackups:\n  daily-at: \"off\"\n"
	if err := os.WriteFile(tpPath, []byte(readFile(t, tpPath)+extra), 0o600); err != nil {
		t.Fatal(err)
	}

	f, code := startFirstRun(t, configPath)
	if code == "" {
		t.Fatal("an unclaimed server logged no setup code")
	}
	sameOrigin := map[string]string{"Origin": "http://" + host, "Content-Type": "application/json"}
	claim := func(remote, code string) *http.Response {
		r := req{remote: remote, method: http.MethodPost, path: access.ClaimPath, headers: sameOrigin, body: `{"code":"` + code + `"}`}
		if remote == "127.0.0.1" {
			r.host, r.headers = "localhost:8317", map[string]string{"Content-Type": "application/json"}
		}
		return f.do(r).Result()
	}
	errCode := func(resp *http.Response) string {
		var body struct{ Error struct{ Code string } }
		_ = json.NewDecoder(resp.Body).Decode(&body)
		return body.Error.Code
	}

	// The new device is told it may claim.
	rec := f.do(req{remote: newIP, path: "/v8/management/config", headers: sameOrigin})
	var denial struct {
		Error struct {
			Code, Node string
			Claimable  bool
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &denial); err != nil || denial.Error.Code != "not_admin" || denial.Error.Node != "new-laptop" || !denial.Error.Claimable {
		t.Fatalf("admin request from a new device while unclaimed = %d %s", rec.Code, rec.Body)
	}

	// Loopback, an address, and the server's own node cannot claim.
	if resp := claim("127.0.0.1", code); resp.StatusCode != http.StatusForbidden || errCode(resp) != "claim_tailnet_only" {
		t.Fatalf("claim from loopback = %d", resp.StatusCode)
	}
	if resp := claim(lanIP, code); resp.StatusCode != http.StatusForbidden || errCode(resp) != "forbidden" {
		t.Fatalf("claim from an access.addresses caller = %d, want the guard's 403", resp.StatusCode)
	}
	if resp := claim(selfIP, code); resp.StatusCode != http.StatusForbidden || errCode(resp) != "forbidden" {
		t.Fatalf("claim from the server's own node = %d, want the guard's 403", resp.StatusCode)
	}
	if resp := claim(newIP, "AAAA-AAAA"); code != "AAAA-AAAA" && (resp.StatusCode != http.StatusForbidden || errCode(resp) != "wrong_code") {
		t.Fatalf("wrong code = %d", resp.StatusCode)
	}
	if resp := claim(newIP, code); resp.StatusCode != http.StatusOK {
		t.Fatalf("claim from the new device = %d", resp.StatusCode)
	}

	// The device now has both surfaces, and taproot.yaml says so.
	if rec := f.do(req{remote: newIP, path: "/v8/management/config", headers: sameOrigin}); rec.Code != http.StatusOK {
		t.Fatalf("admin request after the claim = %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(req{remote: newIP, method: http.MethodPost, path: "/v1/messages", body: `{}`}); isGuardDenial(rec) {
		t.Fatalf("proxy request after the claim denied: %s", rec.Body)
	}
	tp, err := LoadConfig(tpPath)
	if err != nil || len(tp.Access.Devices) != 1 || tp.Access.Devices[0] != (access.DeviceGrant{ID: "nnew-laptopCNTRL", Name: "new-laptop", Proxy: true, Admin: true}) {
		t.Fatalf("taproot.yaml after the claim: %+v, %v\n%s", tp.Access.Devices, err, readFile(t, tpPath))
	}

	// The code is used up: another device gets the guard's not_admin, without claimable.
	resp := claim(workerIP, code)
	if resp.StatusCode != http.StatusForbidden || errCode(resp) != "not_admin" {
		t.Fatalf("second claim = %d, want the guard's not_admin", resp.StatusCode)
	}

	// A restart with an admin device logs no code.
	if _, again := startFirstRun(t, configPath); again != "" {
		t.Fatalf("a claimed server logged setup code %s", again)
	}

	// Recovery: without admin: true entries, the next start logs a new code,
	// and loopback is still an admin.
	written := readFile(t, tpPath)
	if !strings.Contains(written, "admin: true") {
		t.Fatalf("taproot.yaml has no admin entry to remove:\n%s", written)
	}
	if err := os.WriteFile(tpPath, []byte(strings.ReplaceAll(written, "admin: true", "admin: false")), 0o600); err != nil {
		t.Fatal(err)
	}
	f, recovered := startFirstRun(t, configPath)
	if recovered == "" {
		t.Fatal("no setup code after removing every admin entry")
	}
	if rec := f.do(req{remote: "127.0.0.1", host: "localhost:8317", path: "/taproot/api/v1/me"}); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"admin":true`) {
		t.Fatalf("loopback /me after recovery = %d %s", rec.Code, rec.Body)
	}
	if resp := claim(newIP, recovered); resp.StatusCode != http.StatusOK {
		t.Fatalf("claim with the new code = %d", resp.StatusCode)
	}
}
