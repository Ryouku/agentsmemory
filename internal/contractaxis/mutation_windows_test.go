//go:build windows

package contractaxis

import (
	"context"
	"errors"
	"testing"
)

func TestMutationRunnerRefusesWindowsWithoutProcessTreeContainment(t *testing.T) {
	spec := MutationSpec{
		ID: "wire-cut", Axis: "fixture", Item: "*", Case: "*", Patch: "not empty", ExpectedFailure: "expected",
		Compile: Command{Name: "go"}, Assertion: Command{Name: "go"},
	}
	_, err := RunMutation(context.Background(), ".", spec)
	if !errors.Is(err, ErrMutationUnsupported) {
		t.Fatalf("Windows mutation error = %v, want ErrMutationUnsupported", err)
	}
}
