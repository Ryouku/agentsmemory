//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package contractaxis

import (
	"fmt"
	"os/exec"
)

func mutationPlatformError() error {
	return fmt.Errorf("%w: this operating system has no process-tree containment adapter", ErrMutationUnsupported)
}

func configureCommand(*exec.Cmd) {}

func cleanupCommand(*exec.Cmd) error { return nil }
