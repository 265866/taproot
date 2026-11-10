package taproot

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/265866/taproot/internal/config"
)

// The default config.yaml is the shipped example, so the two cannot drift.
func TestDefaultConfigMatchesExample(t *testing.T) {
	example, err := os.ReadFile(filepath.Join("..", "..", "examples", "taproot", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(example) != exampleConfigYAML {
		t.Fatal("exampleConfigYAML differs from examples/taproot/config.yaml; copy the example into firstrun.go")
	}
	if !strings.Contains(exampleConfigYAML, exampleAuthDirLine) {
		t.Fatalf("the example has no line %q to move auth-dir with", exampleAuthDirLine)
	}
}

func runHandleArgs(t *testing.T, args ...string) (exitCode int) {
	t.Helper()
	exitCode = -1
	handleArgs(upstreamFlags(), append([]string{"taproot"}, args...), &bytes.Buffer{}, func(code int) { exitCode = code })
	return exitCode
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// A first start with a missing --config writes both files, which upstream's
// loader and Setup's strict loader then read.
func TestHandleArgsWritesDefaultConfig(t *testing.T) {
	t.Setenv("TAPROOT_CONFIG", "")
	// The directory does not exist yet either.
	dir := filepath.Join(t.TempDir(), "data")
	configPath := filepath.Join(dir, "config.yaml")
	if code := runHandleArgs(t, "--config", configPath); code != -1 {
		t.Fatalf("HandleArgs exited %d", code)
	}

	cfg, err := config.LoadConfigOptional(configPath, false)
	if err != nil {
		t.Fatalf("upstream loader on the written config.yaml: %v", err)
	}
	if want := filepath.Join(dir, "auths"); filepath.Clean(cfg.AuthDir) != want || cfg.Port != 8317 {
		t.Fatalf("written config: auth-dir %q, port %d; want %q and 8317", cfg.AuthDir, cfg.Port, want)
	}
	tpPath := filepath.Join(dir, "taproot.yaml")
	tp, err := LoadConfig(tpPath)
	if err != nil {
		t.Fatalf("LoadConfig on the written taproot.yaml: %v", err)
	}
	if len(tp.Access.Devices) != 0 || len(tp.Access.Addresses) != 0 || tp.Access.Enabled != nil {
		t.Fatalf("written taproot.yaml access = %+v, want an enabled guard with no devices", tp.Access)
	}
	if runtime.GOOS != "windows" {
		for _, p := range []string{configPath, tpPath, dir} {
			info, errStat := os.Stat(p)
			if errStat != nil || info.Mode().Perm()&0o077 != 0 {
				t.Errorf("%s: %v, mode %v; want no group or other access", p, errStat, info.Mode())
			}
		}
	}

	// A second start leaves both files as the first wrote them.
	if errWrite := os.WriteFile(tpPath, []byte("access:\n  devices: []\n"), 0o600); errWrite != nil {
		t.Fatal(errWrite)
	}
	before := readFile(t, configPath)
	if code := runHandleArgs(t, "-config="+configPath); code != -1 {
		t.Fatalf("second HandleArgs exited %d", code)
	}
	if readFile(t, configPath) != before || readFile(t, tpPath) != "access:\n  devices: []\n" {
		t.Fatal("a second start rewrote the config files")
	}
}

// Existing files are never replaced, and an existing config.yaml without
// taproot.yaml stays a startup error (Setup names the missing file).
func TestHandleArgsNeverOverwrites(t *testing.T) {
	t.Setenv("TAPROOT_CONFIG", "")

	t.Run("config.yaml exists", func(t *testing.T) {
		dir := t.TempDir()
		configPath := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(configPath, []byte("port: 9000\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		runHandleArgs(t, "--config", configPath)
		if readFile(t, configPath) != "port: 9000\n" {
			t.Fatal("config.yaml was replaced")
		}
		if _, err := os.Stat(filepath.Join(dir, "taproot.yaml")); !os.IsNotExist(err) {
			t.Fatalf("taproot.yaml beside an existing config.yaml: %v, want none written", err)
		}
	})

	t.Run("taproot.yaml exists", func(t *testing.T) {
		dir := t.TempDir()
		tpPath := filepath.Join(dir, "taproot.yaml")
		const kept = "access:\n  devices:\n    - {id: nEXAMPLE1CNTRL, admin: true}\n"
		if err := os.WriteFile(tpPath, []byte(kept), 0o600); err != nil {
			t.Fatal(err)
		}
		runHandleArgs(t, "--config", filepath.Join(dir, "config.yaml"))
		if readFile(t, tpPath) != kept {
			t.Fatal("taproot.yaml was replaced")
		}
		if !strings.Contains(readFile(t, filepath.Join(dir, "config.yaml")), "auth-dir:") {
			t.Fatal("config.yaml was not written beside the existing taproot.yaml")
		}
	})

	t.Run("TAPROOT_CONFIG", func(t *testing.T) {
		dir := t.TempDir()
		elsewhere := filepath.Join(dir, "settings", "taproot.yaml")
		t.Setenv("TAPROOT_CONFIG", elsewhere)
		runHandleArgs(t, "--config", filepath.Join(dir, "config.yaml"))
		if _, err := LoadConfig(elsewhere); err != nil || !strings.Contains(readFile(t, elsewhere), "setup code") {
			t.Fatalf("taproot.yaml at TAPROOT_CONFIG: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "taproot.yaml")); !os.IsNotExist(err) {
			t.Fatalf("taproot.yaml beside config.yaml with TAPROOT_CONFIG set: %v, want none", err)
		}
	})
}

// --version and the help write nothing, and neither does a run without --config.
func TestHandleArgsWritesNothingForVersionOrHelp(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	for _, args := range [][]string{
		{"--config", configPath, "--version"},
		{"--config", configPath, "-h"},
		{"--config", configPath, "--help"},
		{"--tui"},
		{},
	} {
		runHandleArgs(t, args...)
		for _, d := range []string{dir, cwd} {
			if entries, _ := os.ReadDir(d); len(entries) != 0 {
				t.Fatalf("%q wrote %v into %s", args, entries, d)
			}
		}
	}
}

// A config path that cannot be created stops startup.
func TestHandleArgsExitsWhenItCannotWrite(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if code := runHandleArgs(t, "--config", filepath.Join(blocker, "config.yaml")); code != 1 {
		t.Fatalf("HandleArgs under a regular file exited %d, want 1", code)
	}
}

// unsetEnv removes key for the test, as t.Setenv alone cannot.
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "")
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
}

// main loads .env after HandleArgs, so HandleArgs reads its TAPROOT_CONFIG
// itself, and writes taproot.yaml where Setup will then look.
func TestHandleArgsReadsTaprootConfigFromDotEnv(t *testing.T) {
	unsetEnv(t, "TAPROOT_CONFIG")
	cwd := t.TempDir()
	t.Chdir(cwd)
	elsewhere := filepath.Join(cwd, "etc", "taproot.yaml")
	if err := os.WriteFile(".env", []byte("TAPROOT_CONFIG="+elsewhere+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(cwd, "conf", "config.yaml")
	if code := runHandleArgs(t, "--config", configPath); code != -1 {
		t.Fatalf("HandleArgs exited %d", code)
	}
	if _, err := LoadConfig(elsewhere); err != nil || !strings.Contains(readFile(t, elsewhere), "setup code") {
		t.Fatalf("taproot.yaml at .env's TAPROOT_CONFIG: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cwd, "conf", "taproot.yaml")); !os.IsNotExist(err) {
		t.Fatalf("taproot.yaml beside config.yaml: %v, want none", err)
	}

	// The process environment wins over .env, as it does for main.
	set := filepath.Join(cwd, "set", "taproot.yaml")
	t.Setenv("TAPROOT_CONFIG", set)
	runHandleArgs(t, "--config", filepath.Join(cwd, "other", "config.yaml"))
	if _, err := os.Stat(set); err != nil {
		t.Fatalf("taproot.yaml at the environment's TAPROOT_CONFIG: %v", err)
	}
}

// A run that cannot write taproot.yaml writes no config.yaml either, so the
// next run is a first run again rather than a deployment missing taproot.yaml.
func TestHandleArgsRetriesAFailedFirstRun(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TAPROOT_CONFIG", filepath.Join(blocker, "taproot.yaml"))
	configPath := filepath.Join(dir, "config.yaml")
	if code := runHandleArgs(t, "--config", configPath); code != 1 {
		t.Fatalf("HandleArgs with an unwritable TAPROOT_CONFIG exited %d, want 1", code)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("config.yaml after the failed run: %v, want none", err)
	}

	t.Setenv("TAPROOT_CONFIG", "")
	if code := runHandleArgs(t, "--config", configPath); code != -1 {
		t.Fatalf("second HandleArgs exited %d", code)
	}
	for _, p := range []string{configPath, filepath.Join(dir, "taproot.yaml")} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("after the retry: %v", err)
		}
	}
}

// Only a server run whose flags parse writes the first-run files.
func TestHandleArgsWritesOnlyForAServerRun(t *testing.T) {
	t.Setenv("TAPROOT_CONFIG", "")
	for _, tc := range []struct {
		args  []string
		env   string
		write bool
	}{
		{args: []string{"--no-such-flag"}},
		{args: []string{"--claude-login"}},
		{args: []string{"--codex-login=true"}},
		{args: []string{"--tui"}},
		{args: []string{"--vertex-import", "key.json"}},
		{args: []string{"--oauth-callback-port"}},
		{args: nil, env: "PGSTORE_DSN"},
		{args: nil, env: "gitstore_git_url"},
		{args: []string{"--claude-login=false"}, write: true},
		{args: []string{"--tui=false", "--oauth-callback-port", "9999"}, write: true},
		{args: []string{"serve"}, write: true},
	} {
		dir := t.TempDir()
		configPath := filepath.Join(dir, "config.yaml")
		if tc.env != "" {
			t.Setenv(tc.env, "example")
		}
		runHandleArgs(t, append([]string{"--config", configPath}, tc.args...)...)
		if tc.env != "" {
			unsetEnv(t, tc.env)
		}
		entries, _ := os.ReadDir(dir)
		if wrote := len(entries) != 0; wrote != tc.write {
			t.Errorf("%q with %s set: wrote %v, want writes %t", tc.args, tc.env, entries, tc.write)
		}
	}
}

// Without --config, a missing ./config.yaml gets taproot's hint rather than
// upstream's bare load error, unless the run reads no config file.
func TestHandleArgsHintsAtConfigWithoutIt(t *testing.T) {
	unsetEnv(t, "DEPLOY")
	t.Chdir(t.TempDir())
	for _, args := range [][]string{{}, {"--claude-login"}, {"--tui"}} {
		if code := runHandleArgs(t, args...); code != 1 {
			t.Errorf("%q without config.yaml exited %d, want 1", args, code)
		}
	}
	for _, args := range [][]string{{"--discover"}, {"--discover-json"}, {"-h"}, {"--version"}} {
		if code := runHandleArgs(t, args...); code > 0 {
			t.Errorf("%q without config.yaml exited %d, want no exit or 0", args, code)
		}
	}
	t.Setenv("DEPLOY", "cloud")
	if code := runHandleArgs(t); code != -1 {
		t.Errorf("a cloud deploy without config.yaml exited %d, want no exit", code)
	}
	unsetEnv(t, "DEPLOY")
	if err := os.WriteFile("config.yaml", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if code := runHandleArgs(t); code != -1 {
		t.Errorf("a run beside config.yaml exited %d, want no exit", code)
	}
}
