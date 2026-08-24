// Package cicontract pins the release and preview-image promises made by the
// repository's GitHub Actions workflows.
package cicontract

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPRImageWorkflowPublishesDigestDerivedPRTagWithoutLatest(t *testing.T) {
	workflow := readRepoFile(t, ".github/workflows/pr-image.yml")
	requireText(t, workflow,
		"pull_request:",
		"workflow_dispatch:",
		"github.event.pull_request.head.repo.full_name == github.repository",
		"refs/pull/${pr_number}/head",
		`expected_sha="${INPUT_HEAD_SHA,,}"`,
		`[[ ! "$expected_sha" =~ ^[0-9a-f]{40}$ ]]`,
		`image="ghcr.io/${GITHUB_REPOSITORY,,}-pr"`,
		"push-by-digest=true,name-canonical=true,push=true",
		`tag="pr-${pr_number}-sha256-${digest#sha256:}"`,
		"docker buildx imagetools create --prefer-index=false",
		`test "$tag_digest" = "$digest"`,
		`canonical="${image}@${digest}"`,
		"GITHUB_STEP_SUMMARY",
	)

	for _, forbidden := range []string{
		"docker/metadata-action",
		"type=raw,value=latest",
		"type=semver",
	} {
		if strings.Contains(workflow, forbidden) {
			t.Errorf("PR image workflow contains %q, which can couple previews to a moving release tag", forbidden)
		}
	}
}

func TestPRImageCleanupDeletesOnlyExpiredDigestDerivedPRTags(t *testing.T) {
	workflow := readRepoFile(t, ".github/workflows/pr-image-cleanup.yml")
	requireText(t, workflow,
		"schedule:",
		"workflow_dispatch:",
		"packages: write",
		`cutoff="$(date -u -d '7 days ago'`,
		`package="${GITHUB_REPOSITORY#*/}-pr"`,
		"scripts/select-expired-pr-images.jq",
		`done < "$expired"`,
		`/versions/${version_id}"`,
		`jq -s -e --arg cutoff "$cutoff"`,
		`/orgs/${owner}/packages/container/${package}/versions/${version_id}`,
		"--method DELETE",
	)
}

func TestExpiredPRImageSelectorRejectsProtectedAndFreshVersions(t *testing.T) {
	selector := filepath.Join("..", "..", "scripts", "select-expired-pr-images.jq")
	jq, err := exec.LookPath("jq")
	if err != nil {
		t.Skip("jq is not installed; GitHub's ubuntu runner exercises this selector")
	}

	const fixture = `[
  {"id":1,"created_at":"2026-08-16T00:00:00Z","metadata":{"container":{"tags":["pr-24-sha256-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"]}}},
  {"id":2,"created_at":"2026-08-18T00:00:00Z","metadata":{"container":{"tags":["pr-24-sha256-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"]}}},
  {"id":3,"created_at":"2026-08-16T00:00:00Z","metadata":{"container":{"tags":["latest"]}}},
  {"id":4,"created_at":"2026-08-16T00:00:00Z","metadata":{"container":{"tags":["pr-24-sha256-cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","0.0.92"]}}},
  {"id":5,"created_at":"2026-08-16T00:00:00Z","metadata":{"container":{"tags":[]}}}
]`
	cmd := exec.Command(jq, "-r", "--arg", "cutoff", "2026-08-17T00:00:00Z", "-f", selector)
	cmd.Stdin = strings.NewReader(fixture)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run cleanup selector: %v\n%s", err, out)
	}
	fields := strings.Split(strings.TrimSpace(string(out)), "\t")
	if len(fields) != 3 || fields[0] != "1" {
		t.Fatalf("selected %q, want only expired PR-only version 1", out)
	}
}

func TestHostedComposeAcceptsCanonicalImageDigest(t *testing.T) {
	compose := readRepoFile(t, "docker-compose.prod.yml")
	requireText(t, compose,
		"AGENTSMEMORY_IMAGE",
		`${AGENTSMEMORY_IMAGE:-ghcr.io/atvirokodosprendimai/agentsmemory:${AGENTSMEMORY_IMAGE_TAG:-latest}}`,
	)
}

func TestBuildWorkflowRunsContractAxisGate(t *testing.T) {
	workflow := readRepoFile(t, ".github/workflows/build.yml")
	requireText(t, workflow,
		"go test ./...",
		"-tags contractaxis",
		"./internal/mcptest",
	)
}

func readRepoFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "..", name)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

func requireText(t *testing.T, content string, fragments ...string) {
	t.Helper()
	for _, fragment := range fragments {
		if !strings.Contains(content, fragment) {
			t.Errorf("missing workflow contract %q", fragment)
		}
	}
}
