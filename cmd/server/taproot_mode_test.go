package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Review finding 8: a Home JWT is refused, with a non-zero exit, before main
// parses it or contacts the Home control plane. The test runs main in a child
// process of the test binary.
func TestHomeJWTIsRefusedBeforeHomeActivity(t *testing.T) {
	if os.Getenv("TAPROOT_TEST_RUN_MAIN") == "1" {
		os.Args = append([]string{"taproot"}, strings.Fields(os.Getenv("TAPROOT_TEST_ARGS"))...)
		main()
		return
	}
	dir := t.TempDir()
	config := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(config, []byte("port: 18398\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string]string{
		"HOME_JWT":   "--config " + config,
		"TUI client": "--tui --config " + config,
	} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestHomeJWTIsRefusedBeforeHomeActivity$")
		cmd.Dir = dir
		// The JWT is malformed on purpose: parsing it would fail with
		// "invalid -home-jwt" and exit 0, which is what the refusal must precede.
		cmd.Env = append(os.Environ(), "TAPROOT_TEST_RUN_MAIN=1", "TAPROOT_TEST_ARGS="+args, "HOME_JWT=not.a.jwt")
		out, err := cmd.CombinedOutput()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			t.Fatalf("%s: exit = %v, want status 1\n%s", name, err, out)
		}
		if !strings.Contains(string(out), "does not support Home mode") || strings.Contains(string(out), "invalid -home-jwt") {
			t.Fatalf("%s: output does not show the refusal before JWT parsing:\n%s", name, out)
		}
	}
}
