//go:build unix

package backup

import (
	"fmt"
	"os"
	"syscall"
)

// selfRestart sends SIGTERM to this process. The server shuts down gracefully,
// exits with taproot.RestartExitCode, and its supervisor starts it again.
func selfRestart() error {
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		return fmt.Errorf("signal self: %w", err)
	}
	return nil
}
