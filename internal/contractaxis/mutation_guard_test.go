package contractaxis

import (
	"strings"
	"testing"
)

func TestValidateCommandRejectsRunnerChallengeEnv(t *testing.T) {
	forged := mutationChallengeEnv + "=forged-nonce"
	for _, label := range []string{"compile", "assertion"} {
		got := validateCommand(label, Command{Name: "go", Env: []string{"MODE=test", forged}})
		if got == "" {
			t.Fatalf("%s command with %s in Env was accepted; a spec that sets the challenge forges the kill marker", label, mutationChallengeEnv)
		}
		if !strings.Contains(got, "challenge") {
			t.Fatalf("%s rejection = %q, want it to name the challenge env", label, got)
		}
	}
	if got := validateCommand("assertion", Command{Name: "go", Env: []string{"MODE=test"}}); got != "" {
		t.Fatalf("unrelated env was rejected: %q", got)
	}
}
