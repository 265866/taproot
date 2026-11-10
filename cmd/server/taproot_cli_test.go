package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/265866/taproot/internal/taproot"
	"github.com/265866/taproot/internal/taproot/store"
)

// runMain runs main in a child process of the test binary, in dir, with the
// given arguments, and returns its exit status and combined output. The child
// gets none of the environment variables that pick a store, Home mode, or
// another taproot.yaml.
func runMain(t *testing.T, dir string, args ...string) (int, string) {
	t.Helper()
	out, err := mainCommand(dir, args).CombinedOutput()
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

func mainCommand(dir string, args []string) *exec.Cmd {
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
	return cmd
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

// runningMain is main started by startMain.
type runningMain struct {
	process *os.Process
	exit    <-chan int
	output  *bytes.Buffer
}

// startMain starts main like runMain, without waiting for it, and returns
// once the server on port answers /healthz.
func startMain(t *testing.T, dir, port string, args ...string) runningMain {
	t.Helper()
	cmd := mainCommand(dir, args)
	output := &bytes.Buffer{}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exit := make(chan int, 1)
	go func() {
		var exitErr *exec.ExitError
		switch err := cmd.Wait(); {
		case err == nil:
			exit <- 0
		case errors.As(err, &exitErr):
			exit <- exitErr.ExitCode()
		default:
			exit <- -1
		}
	}()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	m := runningMain{process: cmd.Process, exit: exit, output: output}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case code := <-exit:
			t.Fatalf("main exited %d before serving:\n%s", code, output)
		default:
		}
		if resp, err := http.Get("http://127.0.0.1:" + port + "/healthz"); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return m
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("main did not serve /healthz:\n%s", output)
	return m
}

func (m runningMain) wait(t *testing.T) int {
	t.Helper()
	select {
	case code := <-m.exit:
		return code
	case <-time.After(30 * time.Second):
		t.Fatalf("main did not exit:\n%s", m.output)
		return -1
	}
}

// writeServerConfig writes a config.yaml on a free loopback port, with a
// fake OpenAI-compatible upstream at upstreamURL, and a taproot.yaml with the
// guard off, and returns the port.
func writeServerConfig(t *testing.T, dir, upstreamURL string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	config := "host: 127.0.0.1\nport: " + port + "\nrequest-log: true\nauth-dir: " + filepath.ToSlash(filepath.Join(dir, "auths")) + "\n" +
		"openai-compatibility:\n  - name: fake\n    base-url: " + upstreamURL + "\n    api-key-entries: [{api-key: example-key}]\n    models: [{name: example-model, alias: example-model}]\n"
	tp := "access:\n  enabled: false\ningest:\n  poll-interval: \"0\"\nbackups:\n  daily-at: \"off\"\n"
	for name, content := range map[string]string{"config.yaml": config, "taproot.yaml": tp} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return port
}

// A usage event still queued when the server gets SIGTERM is written before
// the process exits. Ingest writes queued events once a second, and the signal
// follows the response at once.
func TestShutdownKeepsQueuedRecords(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs SIGTERM")
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-1","object":"chat.completion","created":1,"model":"example-model","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`)
	}))
	defer upstream.Close()
	dir := t.TempDir()
	port := writeServerConfig(t, dir, upstream.URL+"/v1")
	m := startMain(t, dir, port, "--config", filepath.Join(dir, "config.yaml"))

	const requests = 3
	for range requests {
		resp, err := http.Post("http://127.0.0.1:"+port+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"example-model","messages":[{"role":"user","content":"hi"}]}`))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST /v1/chat/completions = %d %s\n%s", resp.StatusCode, body, m.output)
		}
	}
	if err := m.process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if code := m.wait(t); code != 0 {
		t.Fatalf("main exited %d after SIGTERM, want 0:\n%s", code, m.output)
	}

	db, err := store.Open(context.Background(), store.Config{}, filepath.Join(dir, "taproot"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for table, want := range map[string]int{"usage_events": requests, "reqlog_records": requests} {
		var n int
		if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != want {
			t.Errorf("%s after SIGTERM = %d, want %d", table, n, want)
		}
	}
}

// A restore makes the server exit with taproot.RestartExitCode, which every
// restart policy restarts, Restart=on-failure included.
func TestRestoreExitsForARestart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("restores restart only on unix")
	}
	dir := t.TempDir()
	port := writeServerConfig(t, dir, "http://127.0.0.1:9/v1")
	m := startMain(t, dir, port, "--config", filepath.Join(dir, "config.yaml"))

	post := func(path string) []byte {
		req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:"+port+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Taproot-Request", "1")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode >= 300 {
			t.Fatalf("POST %s = %d %s\n%s", path, resp.StatusCode, body, m.output)
		}
		return body
	}
	var backup struct{ ID string }
	if err := json.Unmarshal(post("/taproot/api/v1/backups"), &backup); err != nil || backup.ID == "" {
		t.Fatalf("create backup: %v", err)
	}
	post("/taproot/api/v1/backups/" + backup.ID + "/restore")
	if code := m.wait(t); code != taproot.RestartExitCode {
		t.Fatalf("main exited %d after a restore, want %d:\n%s", code, taproot.RestartExitCode, m.output)
	}
}
