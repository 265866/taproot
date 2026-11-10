package taproot

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/265866/taproot/internal/buildinfo"
)

// upstreamFlags defines a sample of upstream's flags of each kind.
func upstreamFlags() *flag.FlagSet {
	fs := flag.NewFlagSet("taproot", flag.ContinueOnError)
	fs.String("config", "", "Configure File Path")
	fs.Bool("tui", false, "Start with terminal management UI")
	fs.Bool("standalone", false, "In TUI mode, start an embedded local server")
	fs.String("home-jwt", "", "Home control plane JWT for mTLS certificate bootstrap and connection")
	fs.Bool("home-disable-cluster-discovery", false, "Disable Home CLUSTER NODES discovery")
	fs.String("password", "", "")
	fs.Bool("claude-login", false, "Login to Claude using OAuth")
	fs.Bool("codex-login", false, "Login to Codex using OAuth")
	fs.Bool("kimi-login", false, "Login to Kimi (.com) using OAuth")
	fs.Bool("devin-login", false, "Login to Devin using OAuth")
	fs.String("vertex-import", "", "Import Vertex service account key JSON file")
	fs.String("siwc-bind", "", "Bind Sign in with ChatGPT (SIWC) credentials to a Codex credential file")
	fs.Int("oauth-callback-port", 0, "Override OAuth callback port")
	fs.Int("discover-timeout", 3, "Timeout in seconds for LAN discovery")
	fs.Bool("discover", false, "Discover local AI gateways")
	fs.Bool("discover-json", false, "Output discovered gateways in JSON format")
	return fs
}

// chdirWithConfig runs the test in a directory with a config.yaml, where a run
// without --config is not refused.
func chdirWithConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
}

func TestVersionLine(t *testing.T) {
	saved := [3]string{buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate}
	t.Cleanup(func() { buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate = saved[0], saved[1], saved[2] })
	buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate = "v0.2.0", "abc1234", "2026-11-08T00:00:00Z"
	if got, want := VersionLine(), "taproot v0.2.0 (commit abc1234, built 2026-11-08T00:00:00Z)"; got != want {
		t.Fatalf("VersionLine() = %q, want %q", got, want)
	}
}

func TestHandleArgsVersion(t *testing.T) {
	// "--config --version" names a missing config file, which HandleArgs creates.
	chdirWithConfig(t)
	for _, tc := range []struct {
		args string
		want bool
	}{
		{"--version", true},
		{"-version", true},
		{"--version=true", true},
		{"--version=false", false},
		{"--version=maybe", false},
		{"--config /data/config.yaml --version", true},
		{"--config=/data/config.yaml -version", true},
		{"--tui --version", true},
		{"--version --no-such-flag", true},
		// The flag package reads these as a value, a positional argument, or the end of flags.
		{"--config --version", false},
		{"serve --version", false},
		{"-- --version", false},
		{"- --version", false},
		// flag.Parse stops at an undefined flag (-h prints the help), so --version after it is never seen.
		{"-h --version", false},
		{"--no-such-flag --version", false},
		{"", false},
	} {
		fs := upstreamFlags()
		var out bytes.Buffer
		exitCode := -1
		args := append([]string{"taproot"}, strings.Fields(tc.args)...)
		handleArgs(fs, args, &out, func(code int) { exitCode = code })
		if tc.want {
			if exitCode != 0 || out.String() != VersionLine()+"\n" {
				t.Errorf("%q: exit %d, output %q; want exit 0 and %q", tc.args, exitCode, out.String(), VersionLine())
			}
			continue
		}
		if exitCode != -1 || out.Len() != 0 {
			t.Errorf("%q: exit %d, output %q; want neither", tc.args, exitCode, out.String())
		}
		// The same arguments must not make flag.Parse set -version either.
		fs.SetOutput(&bytes.Buffer{})
		if err := fs.Parse(args[1:]); err == nil && fs.Lookup("version").Value.String() == "true" {
			t.Errorf("%q: flag.Parse set -version, but HandleArgs did not serve it", tc.args)
		}
	}
}

// An upstream flag named version is left alone rather than redefined, and
// --version is upstream's to serve.
func TestHandleArgsKeepsAnExistingVersionFlag(t *testing.T) {
	chdirWithConfig(t)
	for _, args := range [][]string{{"taproot"}, {"taproot", "--version", "1.2"}, {"taproot", "--version=1.2"}} {
		fs := upstreamFlags()
		fs.String("version", "", "upstream's own version flag")
		exited := false
		var out bytes.Buffer
		handleArgs(fs, args, &out, func(int) { exited = true })
		if exited || out.Len() != 0 || fs.Lookup("version").Usage != "upstream's own version flag" {
			t.Fatalf("%q: exited %t, printed %q, version flag %+v", args, exited, out.String(), fs.Lookup("version"))
		}
		want := ""
		if len(args) > 1 {
			want = "1.2"
		}
		if err := fs.Parse(args[1:]); err != nil || fs.Lookup("version").Value.String() != want {
			t.Fatalf("%q: Parse = %v, version %q, want %q", args, err, fs.Lookup("version").Value, want)
		}
	}
}

func TestHandleArgsUsage(t *testing.T) {
	chdirWithConfig(t)
	fs := upstreamFlags()
	handleArgs(fs, []string{"taproot"}, &bytes.Buffer{}, func(int) { t.Fatal("exited without --version") })
	var out bytes.Buffer
	fs.SetOutput(&out)
	if err := fs.Parse([]string{"-h"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("Parse(-h) = %v, want flag.ErrHelp", err)
	}
	help := out.String()
	if !strings.HasPrefix(help, "Usage: taproot [flags]\n") {
		t.Errorf("help does not start with taproot's usage line:\n%s", help)
	}
	for _, want := range []string{"\n  -config string\n", "\n  -claude-login\n", "\n  -codex-login\n", "\n  -tui\n", "\n  -version\n", "Print the version and exit", "TAPROOT_CONFIG", "(default 3)"} {
		if !strings.Contains(help, want) {
			t.Errorf("help lacks %q:\n%s", want, help)
		}
	}
	for _, hidden := range []string{"home-jwt", "home-disable-cluster-discovery", "standalone", "password", "kimi-login", "devin-login", "vertex-import", "siwc-bind", "CLIProxyAPI"} {
		if strings.Contains(help, hidden) {
			t.Errorf("help lists %q:\n%s", hidden, help)
		}
	}
	// Hidden flags still parse, so upstream's code paths keep working.
	if err := fs.Parse([]string{"-standalone", "-kimi-login"}); err != nil {
		t.Fatalf("hidden flags no longer parse: %v", err)
	}
}
