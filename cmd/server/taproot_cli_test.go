package main

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/265866/taproot/internal/taproot"
)

// runMain runs main in a child process of the test binary, in dir, with the
// given arguments, and returns its exit status and combined output. The child
// gets none of the environment variables that pick a store, Home mode, or
// another taproot.yaml.
func runMain(t *testing.T, dir string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestRunMain$")
	cmd.Dir = dir
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch {
		case name == "TAPROOT_CONFIG", name == "DEPLOY", name == "MANAGEMENT_PASSWORD",
			strings.HasSuffix(strings.ToUpper(name), "_PROXY"),
			strings.HasPrefix(strings.ToUpper(name), "HOME_JWT"),
			strings.HasPrefix(strings.ToUpper(name), "PGSTORE_"),
			strings.HasPrefix(strings.ToUpper(name), "GITSTORE_"),
			strings.HasPrefix(strings.ToUpper(name), "OBJECTSTORE_"):
			continue
		}
		cmd.Env = append(cmd.Env, kv)
	}
	// Startup fetches (model catalogs, client versions) fail fast at a dead proxy.
	dead := "http://127.0.0.1:9"
	cmd.Env = append(cmd.Env, "HTTP_PROXY="+dead, "HTTPS_PROXY="+dead, "http_proxy="+dead, "https_proxy="+dead, "NO_PROXY=127.0.0.1,localhost", "no_proxy=127.0.0.1,localhost",
		"TAPROOT_TEST_RUN_MAIN=1", "TAPROOT_TEST_ARGS="+strings.Join(args, "\x1f"))
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, string(out)
	case errors.As(err, &exit):
		return exit.ExitCode(), string(out)
	default:
		t.Fatalf("run main %v: %v", args, err)
		return -1, ""
	}
}

// TestRunMain is the child process of runMain.
func TestRunMain(t *testing.T) {
	if os.Getenv("TAPROOT_TEST_RUN_MAIN") != "1" {
		t.Skip("runs only as the child process of runMain")
	}
	os.Args = append([]string{"taproot"}, strings.Split(os.Getenv("TAPROOT_TEST_ARGS"), "\x1f")...)
	main()
}

func TestVersionFlagPrintsTaprootVersion(t *testing.T) {
	for _, flag := range []string{"--version", "-version"} {
		code, out := runMain(t, t.TempDir(), flag)
		// The test binary prints PASS after main returns; --version exits before that.
		if code != 0 || out != taproot.VersionLine()+"\n" {
			t.Errorf("%s: exit %d, output %q; want exit 0 and only %q", flag, code, out, taproot.VersionLine())
		}
	}
}

func TestHelpListsTaprootFlags(t *testing.T) {
	code, out := runMain(t, t.TempDir(), "--help")
	if code != 0 || !strings.Contains(out, taproot.VersionLine()+"\nUsage: taproot [flags]\n") {
		t.Fatalf("--help: exit %d, want 0 and taproot's usage:\n%s", code, out)
	}
	for _, want := range []string{"\n  -config string\n", "\n  -claude-login\n", "\n  -codex-login\n", "\n  -tui\n", "\n  -version\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("--help lacks %q:\n%s", want, out)
		}
	}
	for _, hidden := range []string{"-home-jwt", "-standalone", "-kimi-login", "-devin-login", "-vertex-import", "-siwc-bind", "-password", "CLIProxyAPI"} {
		if strings.Contains(out, hidden) {
			t.Errorf("--help lists %q:\n%s", hidden, out)
		}
	}
	if code, out := runMain(t, t.TempDir(), "--no-such-flag"); code != 2 || !strings.Contains(out, "Usage: taproot [flags]") {
		t.Errorf("unknown flag: exit %d, want 2 with the usage:\n%s", code, out)
	}
}

// A server that cannot start exits nonzero, so a supervisor sees the failure.
func TestFatalStartupErrorsExitNonzero(t *testing.T) {
	write := func(t *testing.T, path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name    string
		config  string
		taproot string // "" leaves taproot.yaml out
		want    func(dir string) string
	}{
		{
			name:   "config does not parse",
			config: "port: [\n",
			want:   func(string) string { return "failed to load config" },
		},
		{
			name:   "taproot.yaml missing",
			config: "port: 18397\n",
			want: func(dir string) string {
				return "taproot.yaml not found at " + filepath.Join(dir, "taproot.yaml")
			},
		},
		{
			name:    "tailscaled socket missing",
			config:  "port: 18397\n",
			taproot: "access:\n  tailscale-socket: SOCKET\n",
			want: func(dir string) string {
				return "tailscaled socket " + filepath.Join(dir, "no-tailscaled.sock") + " does not exist"
			},
		},
		{
			name: "port in use",
			// Everything outbound goes to a dead loopback proxy.
			config:  "host: 127.0.0.1\nport: PORT\nproxy-url: http://127.0.0.1:9\n",
			taproot: "access:\n  enabled: false\ningest:\n  poll-interval: \"0\"\nbackups:\n  daily-at: \"off\"\n",
			want:    func(string) string { return "address already in use" },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			config := filepath.Join(dir, "config.yaml")
			taken, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = taken.Close() }()
			port := strconv.Itoa(taken.Addr().(*net.TCPAddr).Port)
			write(t, config, strings.ReplaceAll(tc.config, "PORT", port)+"auth-dir: "+filepath.ToSlash(filepath.Join(dir, "auths"))+"\n")
			if tc.taproot != "" {
				socket := filepath.ToSlash(filepath.Join(dir, "no-tailscaled.sock"))
				write(t, filepath.Join(dir, "taproot.yaml"), strings.ReplaceAll(tc.taproot, "SOCKET", socket))
			}
			code, out := runMain(t, dir, "--config", config)
			if code != 1 || !strings.Contains(out, tc.want(dir)) {
				t.Fatalf("exit %d, want 1 with %q:\n%s", code, tc.want(dir), out)
			}
		})
	}
}
