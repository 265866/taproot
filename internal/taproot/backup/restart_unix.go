//go:build unix

package backup

import (
	"fmt"
	"os"
	"syscall"
)

// selfRestart sends SIGTERM to this process. The server shuts down gracefully
// and the container's restart policy starts it again.
func selfRestart() error {
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		return fmt.Errorf("signal self: %w", err)
	}
	return nil
}
