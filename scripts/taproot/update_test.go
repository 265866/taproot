package taprootscripts

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeHostTools stands in for podman, systemctl, curl, cosign, and sleep, so
// taproot-update runs against a simulated host and registry. Its state is a
// directory of small files (FAKE_STATE):
//
//	track          digest the registry's track tag points to
//	registry/<d>   image ID that pulling digest <d> yields
//	signed/<d>     present when digest <d> carries a valid release signature
//	tags/<ref>     image ID a local tag points to
//	digests/<id>   repository digests of image <id>, one per line
//	running        image ID the container runs
//	active         present while the unit is active
//	healthy/<id>   present when the container answers health checks on <id>
//	stuck          present when a restart fails and leaves the container as is
//	calls          one line per state-changing call, in order
const fakeHostTools = `#!/usr/bin/env bash
set -u
s=$FAKE_STATE
key() { printf '%s' "$1" | tr '/:@' '___'; }
call() { printf '%s\n' "$*" >>"$s/calls"; }
case $(basename "$0") in
podman)
	case "$1 $2" in
	"image inspect")
		ref=${*: -1} format=$4
		if [[ $ref == *@sha256:* ]]; then
			id=$(cat "$s/pulled/$(key "${ref#*@}")" 2>/dev/null) || exit 125
		else
			id=$(cat "$s/tags/$(key "$ref")" 2>/dev/null) || exit 125
		fi
		if [[ $format == *RepoDigests* ]]; then
			cat "$s/digests/$id" 2>/dev/null
		else
			echo "$id"
		fi ;;
	"container inspect")
		[[ -f $s/running ]] || exit 125
		echo "true $(cat "$s/running")" ;;
	"pull -q")
		ref=$3 d=${3#*@}
		call "podman pull $ref"
		id=$(cat "$s/registry/$(key "$d")" 2>/dev/null) || exit 125
		mkdir -p "$s/pulled" "$s/digests"
		echo "$id" >"$s/pulled/$(key "$d")"
		echo "$ref" >>"$s/digests/$id"
		echo "$id" ;;
	"tag "*)
		call "podman tag $2 $3"
		mkdir -p "$s/tags"
		echo "$2" >"$s/tags/$(key "$3")" ;;
	"image rm")
		call "podman image rm $3" ;;
	"untag "*)
		call "podman untag $2 $3"
		rm "$s/tags/$(key "$3")" ;;
	*) echo "fake podman: unexpected $*" >&2; exit 99 ;;
	esac ;;
systemctl)
	case $2 in
	is-active) [[ -f $s/active ]] ;;
	restart)
		call "systemctl restart $3"
		[[ -f $s/stuck ]] && exit 1
		cp "$s/tags/$(key localhost/taproot:current)" "$s/running" ;;
	*) echo "fake systemctl: unexpected $*" >&2; exit 99 ;;
	esac ;;
curl)
	out="" url="" head=false auth=false
	while (($#)); do
		case $1 in
		-o) out=$2; shift ;;
		-H) [[ $2 == Authorization:* ]] && auth=true; shift ;;
		--max-time | --data-urlencode) shift ;;
		-I) head=true ;;
		-*) ;;
		*) url=$1 ;;
		esac
		shift
	done
	if $head; then
		if $auth; then
			printf 'HTTP/2 200\r\ndocker-content-digest: %s\r\n\r\n' "$(cat "$s/track")" >"$out"
		else
			printf 'HTTP/2 401\r\nwww-authenticate: Bearer realm="https://registry.example.com/token",service="registry.example.com",scope="repository:265866/taproot:pull"\r\n\r\n' >"$out"
		fi
	elif [[ $url == https://registry.example.com/token ]]; then
		echo '{"token":"t"}' >"$out"
	elif [[ $url == http://127.0.0.1:18417/healthz ]]; then
		[[ -f $s/running && -f $s/healthy/$(cat "$s/running") ]] || exit 7
	elif [[ $url == https://github.com/sigstore/cosign/* ]]; then
		call "download $url"
		echo 'not cosign' >"$out"
	else
		echo "fake curl: unexpected $url" >&2; exit 99
	fi ;;
cosign)
	ref=${*: -1}
	call "cosign verify $ref"
	[[ $* == *"--certificate-github-workflow-repository 265866/taproot"* ]] || exit 98
	[[ -f $s/signed/$(key "${ref#*@}") ]] || { echo "no matching signatures" >&2; exit 1; } ;;
sleep) ;;
esac
`

const (
	digestOld = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	digestNew = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	idOld     = "0ld0000000000000000000000000000000000000000000000000000000000000"
	idNew     = "4e30000000000000000000000000000000000000000000000000000000000000"
	idOlder   = "01de000000000000000000000000000000000000000000000000000000000000"
	image     = "registry.example.com/265866/taproot"
)

type updateHost struct {
	t       *testing.T
	dir     string
	state   string
	config  string
	failed  string
	baseEnv []string
}

// newUpdateHost simulates a host whose unit runs idOld (pulled as digestOld)
// healthily, with the track tag moved to digestNew.
func newUpdateHost(t *testing.T) *updateHost {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not installed")
	}
	dir := t.TempDir()
	h := &updateHost{t: t, dir: dir, state: filepath.Join(dir, "state")}
	bin := filepath.Join(dir, "bin")
	h.write("bin/fake", fakeHostTools)
	if err := os.Chmod(filepath.Join(bin, "fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"podman", "systemctl", "curl", "cosign", "sleep"} {
		if err := os.Symlink("fake", filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	h.config = filepath.Join(dir, "update.env")
	h.setConfig("COSIGN=cosign\n")
	h.failed = filepath.Join(dir, "xdg-state", "taproot-update", "taproot-update-test.service.failed")
	h.baseEnv = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_STATE="+h.state,
		"HOME="+filepath.Join(dir, "home"),
		"XDG_STATE_HOME="+filepath.Join(dir, "xdg-state"),
		"XDG_DATA_HOME="+filepath.Join(dir, "xdg-data"),
		"XDG_RUNTIME_DIR="+dir,
	)

	h.write("state/track", digestNew)
	h.write("state/registry/"+key(digestOld), idOld)
	h.write("state/registry/"+key(digestNew), idNew)
	h.write("state/signed/"+key(digestOld), "")
	h.write("state/signed/"+key(digestNew), "")
	h.write("state/tags/"+key("localhost/taproot:current"), idOld)
	h.write("state/tags/"+key("localhost/taproot:previous"), idOlder)
	h.write("state/digests/"+idOld, image+"@"+digestOld+"\n")
	h.write("state/running", idOld)
	h.write("state/active", "")
	h.write("state/healthy/"+idOld, "")
	h.write("state/healthy/"+idNew, "")
	h.write("state/calls", "")
	return h
}

func key(ref string) string { return strings.NewReplacer("/", "_", ":", "_", "@", "_").Replace(ref) }

func (h *updateHost) write(rel, content string) {
	h.t.Helper()
	path := filepath.Join(h.dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

func (h *updateHost) remove(rel string) {
	h.t.Helper()
	if err := os.Remove(filepath.Join(h.dir, rel)); err != nil {
		h.t.Fatal(err)
	}
}

func (h *updateHost) read(rel string) string {
	h.t.Helper()
	data, err := os.ReadFile(filepath.Join(h.dir, rel))
	if err != nil {
		h.t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

// setConfig writes the settings file with the test unit's settings followed
// by extra lines.
func (h *updateHost) setConfig(extra string) {
	h.t.Helper()
	h.write("update.env", `# test settings
TAPROOT_IMAGE=`+image+`
TAPROOT_TRACK="0.2"
TAPROOT_UNIT=taproot-update-test.service
TAPROOT_CONTAINER=taproot-update-test
TAPROOT_LOCAL_TAG=localhost/taproot:current
TAPROOT_HEALTH_URL=http://127.0.0.1:18417/healthz
TAPROOT_HEALTH_TIMEOUT=10
`+extra)
}

// run runs taproot-update and returns its output, exit code, and the
// state-changing calls it made, which are then cleared.
func (h *updateHost) run(args ...string) (string, int, []string) {
	h.t.Helper()
	cmd := exec.Command(scriptPath(h.t, "taproot-update"), append([]string{"--config", h.config}, args...)...)
	cmd.Env = h.baseEnv
	out, err := cmd.CombinedOutput()
	code := 0
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		code = exit.ExitCode()
	default:
		h.t.Fatalf("taproot-update: %v", err)
	}
	var calls []string
	if logged := h.read("state/calls"); logged != "" {
		calls = strings.Split(logged, "\n")
	}
	h.write("state/calls", "")
	return string(out), code, calls
}

func (h *updateHost) tag(ref string) string {
	h.t.Helper()
	return h.read("state/tags/" + key(ref))
}

func wantCalls(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func wantOutput(t *testing.T, out string, parts ...string) {
	t.Helper()
	for _, part := range parts {
		if !strings.Contains(out, part) {
			t.Fatalf("output lacks %q:\n%s", part, out)
		}
	}
}

func TestUpdateDoesNothingWhenTheDigestMatches(t *testing.T) {
	h := newUpdateHost(t)
	h.write("state/track", digestOld)
	out, code, calls := h.run()
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wantCalls(t, calls)
	wantOutput(t, out, "is already "+image+"@"+digestOld)
}

func TestUpdateAppliesAVerifiedRelease(t *testing.T) {
	h := newUpdateHost(t)
	out, code, calls := h.run()
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	target := image + "@" + digestNew
	wantCalls(t, calls,
		"cosign verify "+target,
		"podman pull "+target,
		"podman tag "+idOld+" localhost/taproot:previous",
		"podman tag "+idNew+" localhost/taproot:current",
		"systemctl restart taproot-update-test.service",
		"podman image rm "+idOlder,
	)
	if got := h.tag("localhost/taproot:current"); got != idNew {
		t.Fatalf("current tag = %s, want %s", got, idNew)
	}
	if got := h.read("state/running"); got != idNew {
		t.Fatalf("running %s, want %s", got, idNew)
	}
	wantOutput(t, out, "updated taproot-update-test.service to "+target)

	// The next run finds the new digest in place.
	out, code, calls = h.run()
	if code != 0 {
		t.Fatalf("second run exit %d:\n%s", code, out)
	}
	wantCalls(t, calls)
}

func TestUpdateRefusesAnUnverifiedRelease(t *testing.T) {
	h := newUpdateHost(t)
	h.remove("state/signed/" + key(digestNew))
	out, code, calls := h.run()
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out)
	}
	wantCalls(t, calls, "cosign verify "+image+"@"+digestNew)
	wantOutput(t, out, "refusing "+image+"@"+digestNew, "cosign: no matching signatures")
	if got := h.tag("localhost/taproot:current"); got != idOld {
		t.Fatalf("current tag moved to %s", got)
	}
}

func TestUpdateRollsBackAFailedHealthCheck(t *testing.T) {
	h := newUpdateHost(t)
	h.remove("state/healthy/" + idNew)
	out, code, calls := h.run()
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out)
	}
	target := image + "@" + digestNew
	wantCalls(t, calls,
		"cosign verify "+target,
		"podman pull "+target,
		"podman tag "+idOld+" localhost/taproot:previous",
		"podman tag "+idNew+" localhost/taproot:current",
		"systemctl restart taproot-update-test.service",
		"podman tag "+idOld+" localhost/taproot:current",
		"podman tag "+idOlder+" localhost/taproot:previous",
		"systemctl restart taproot-update-test.service",
		"podman image rm "+idNew,
	)
	wantOutput(t, out, "rolled back taproot-update-test.service to "+idOld+" after "+target+" failed its health check")
	if got := h.read("state/running"); got != idOld {
		t.Fatalf("running %s after rollback, want %s", got, idOld)
	}
	if got := h.tag("localhost/taproot:previous"); got != idOlder {
		t.Fatalf("previous tag = %s after rollback, want %s back", got, idOlder)
	}
	if got, err := os.ReadFile(h.failed); err != nil || strings.TrimSpace(string(got)) != digestNew {
		t.Fatalf("failed digest file = %q, %v", got, err)
	}

	// The failed release is not retried, but the next one is.
	out, code, calls = h.run()
	if code != 0 {
		t.Fatalf("rerun exit %d:\n%s", code, out)
	}
	wantCalls(t, calls)
	wantOutput(t, out, "skipping "+target)

	const digestNext = "sha256:3333333333333333333333333333333333333333333333333333333333333333"
	const idNext = "3e70000000000000000000000000000000000000000000000000000000000000"
	h.write("state/track", digestNext)
	h.write("state/registry/"+key(digestNext), idNext)
	h.write("state/signed/"+key(digestNext), "")
	h.write("state/healthy/"+idNext, "")
	out, code, _ = h.run()
	if code != 0 {
		t.Fatalf("next release exit %d:\n%s", code, out)
	}
	if got := h.read("state/running"); got != idNext {
		t.Fatalf("running %s, want %s", got, idNext)
	}
	if _, err := os.Stat(h.failed); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed digest file still present after a successful update: %v", err)
	}
}

func TestUpdateRollbackLeavesNoPreviousTagWhenThereWasNone(t *testing.T) {
	h := newUpdateHost(t)
	h.remove("state/tags/" + key("localhost/taproot:previous"))
	h.remove("state/healthy/" + idNew)
	out, code, _ := h.run()
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(h.state, "tags", key("localhost/taproot:previous"))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("previous tag left behind after rollback: %v", err)
	}
	if got := h.tag("localhost/taproot:current"); got != idOld {
		t.Fatalf("current tag = %s, want %s", got, idOld)
	}
}

// A restart that fails leaves the old container answering the health URL,
// which must not pass for the new release.
func TestUpdateRollsBackWhenTheContainerKeepsTheOldImage(t *testing.T) {
	h := newUpdateHost(t)
	h.write("state/stuck", "")
	out, code, _ := h.run()
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out)
	}
	wantOutput(t, out, "systemctl --user restart taproot-update-test.service failed", "rolled back")
	if got := h.tag("localhost/taproot:current"); got != idOld {
		t.Fatalf("current tag = %s, want %s", got, idOld)
	}
}

func TestUpdateExitsTwoWhenTheRollbackIsUnhealthyToo(t *testing.T) {
	h := newUpdateHost(t)
	h.remove("state/healthy/" + idNew)
	h.remove("state/healthy/" + idOld)
	out, code, _ := h.run()
	if code != 2 {
		t.Fatalf("exit %d, want 2:\n%s", code, out)
	}
	wantOutput(t, out, "not healthy after the rollback")
	if got := h.tag("localhost/taproot:current"); got != idOld {
		t.Fatalf("current tag = %s after rollback, want %s", got, idOld)
	}
}

func TestUpdateCheckChangesNothing(t *testing.T) {
	h := newUpdateHost(t)
	out, code, calls := h.run("--check")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wantCalls(t, calls, "cosign verify "+image+"@"+digestNew)
	wantOutput(t, out, "would update taproot-update-test.service from "+idOld+" to "+image+"@"+digestNew)

	h.remove("state/signed/" + key(digestNew))
	out, code, _ = h.run("--check")
	if code != 1 {
		t.Fatalf("unsigned --check exit %d, want 1:\n%s", code, out)
	}
	wantOutput(t, out, "would refuse")

	// Without a cosign setting, --check downloads nothing.
	h.setConfig("")
	out, code, calls = h.run("--check")
	if code != 0 {
		t.Fatalf("--check without cosign exit %d:\n%s", code, out)
	}
	wantCalls(t, calls)
	wantOutput(t, out, "cosign v3.1.4 is not installed yet")
}

func TestUpdateRefusesACosignDownloadWithTheWrongChecksum(t *testing.T) {
	h := newUpdateHost(t)
	h.setConfig("")
	out, code, calls := h.run()
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out)
	}
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "download https://github.com/sigstore/cosign/releases/download/v3.1.4/cosign-linux-") {
		t.Fatalf("calls = %q, want only the cosign download", calls)
	}
	wantOutput(t, out, "does not match its pinned SHA-256")
	if entries, _ := os.ReadDir(filepath.Join(h.dir, "xdg-data", "taproot-update")); len(entries) != 0 {
		t.Fatalf("a rejected cosign download was kept: %v", entries)
	}
}

func TestUpdateOnlyRetagsAStoppedUnit(t *testing.T) {
	h := newUpdateHost(t)
	h.remove("state/active")
	out, code, calls := h.run()
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	for _, c := range calls {
		if strings.HasPrefix(c, "systemctl") {
			t.Fatalf("restarted a stopped unit: %q", calls)
		}
	}
	if got := h.tag("localhost/taproot:current"); got != idNew {
		t.Fatalf("current tag = %s, want %s", got, idNew)
	}
	wantOutput(t, out, "is not running, so it starts on the new image next time")
}

func TestUpdateRejectsBadSettings(t *testing.T) {
	for name, extra := range map[string]string{
		"unknown key":     "TAPROOT_TYPO=1\n",
		"previous tag":    "TAPROOT_LOCAL_TAG=localhost/taproot:previous\n",
		"untagged local":  "TAPROOT_LOCAL_TAG=localhost/taproot\n",
		"short image":     "TAPROOT_IMAGE=265866/taproot\n",
		"bad timeout":     "TAPROOT_HEALTH_TIMEOUT=soon\n",
		"not key value":   "just words\n",
		"digest as track": "TAPROOT_TRACK=sha256:abc\n",
	} {
		t.Run(name, func(t *testing.T) {
			h := newUpdateHost(t)
			h.setConfig(extra)
			out, code, calls := h.run()
			if code != 1 {
				t.Fatalf("exit %d, want 1:\n%s", code, out)
			}
			wantCalls(t, calls)
		})
	}
}

func TestUpdateAcceptsTheExampleSettings(t *testing.T) {
	h := newUpdateHost(t)
	h.config = filepath.Join("..", "..", "examples", "taproot", "taproot-update.env")
	out, code, calls := h.run("--check")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wantCalls(t, calls)
	wantOutput(t, out, "would update taproot.service from "+idOld+" to ghcr.io/265866/taproot@"+digestNew)
}

// TestUpdateScriptIsShellcheckClean runs shellcheck when it is installed, as
// it is on GitHub's Ubuntu runners.
func TestUpdateScriptIsShellcheckClean(t *testing.T) {
	shellcheck, err := exec.LookPath("shellcheck")
	if err != nil {
		t.Skip("shellcheck is not installed")
	}
	if out, err := exec.Command(shellcheck, scriptPath(t, "taproot-update")).CombinedOutput(); err != nil {
		t.Fatalf("shellcheck: %v\n%s", err, out)
	}
}
