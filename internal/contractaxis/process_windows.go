//go:build windows

package contractaxis

import (
	"fmt"
	"os"
	"os/exec"
	"time"
)

func mutationPlatformError() error {
	return fmt.Errorf("%w: Windows process trees require Job Object containment", ErrMutationUnsupported)
}

func configureCommand(cmd *exec.Cmd) {
	// RunMutation rejects Windows before starting a command. Keep the direct
	// cancellation fallback for compilation and any future internal diagnostics.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = 2 * time.Second
}

func cleanupCommand(*exec.Cmd) error { return nil }
