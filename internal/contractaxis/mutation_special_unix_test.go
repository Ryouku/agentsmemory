//go:build darwin || linux

package contractaxis

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestMutationCommandCannotFollowASymlinkOutsideTheWorktree(t *testing.T) {
	repo := newMutationFixture(t, false)
	if err := os.Symlink("..", filepath.Join(repo, "inside")); err != nil {
		t.Fatalf("create escaping symlink: %v", err)
	}
	runFixtureGit(t, repo, "add", "inside")
	runFixtureGit(t, repo, "-c", "user.name=Contract Axis", "-c", "user.email=contract-axis@example.invalid", "commit", "-qm", "add symlink")
	spec := mutationSpec(falsePatch())
	spec.Compile.Dir = "inside"

	_, err := RunMutation(context.Background(), repo, spec)
	if err == nil || !strings.Contains(err.Error(), "resolves outside") {
		t.Fatalf("symlink escape error = %v", err)
	}
	assertFixtureClean(t, repo)
}

func TestTreeDigestRejectsAFIFOWithoutOpeningIt(t *testing.T) {
	root := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(root, "generated.pipe"), 0o600); err != nil {
		t.Fatalf("create FIFO: %v", err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := treeDigest(root)
		result <- err
	}()

	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "special file") {
			t.Fatalf("FIFO digest error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("tree digest blocked while opening a FIFO")
	}
}
