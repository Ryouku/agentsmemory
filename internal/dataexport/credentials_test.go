package dataexport

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// credentialColumn matches the names this project gives to secret material. It
// is deliberately broad: a false positive costs one line in publicByReview and a
// sentence saying why, while a false negative ships a live credential to whoever
// asks for their data.
var credentialColumn = regexp.MustCompile(`(?i)(secret|password|passwd|token|api_?key|private_?key|credential|salt|nonce|signature|session_key)`)

// publicByReview lists columns whose names look like credentials and are not.
// Each needs a reason, because the reason is the review — a bare allowlist is a
// place to put things you have not thought about.
var publicByReview = map[string]string{
	// The OAuth client identifier. Public by definition: it travels in the
	// authorization URL and identifies the client to the user, who is the one
	// receiving this export.
	"api_keys.client_key": "public OAuth client id, not a secret",
	// A boolean flag, not the secret it guards.
	"users.totp_enabled": "INTEGER flag recording whether TOTP is on",
}

// TestExportedCredentialColumnsAreRedacted fails when a table in the export
// manifest grows a credential-shaped column that nothing blanks.
//
// This is not hypothetical. copyTable reads every column (`SELECT *`) while
// `redactors` is maintained by hand, so a migration that adds a secret to an
// exported table ships it silently — nothing fails, nothing warns, and the
// archive looks exactly as it did before. That happened: migration 00017 added
// `users.totp_secret`, the base32 shared secret an authenticator app is seeded
// with, years after the map was last extended. A data-subject export therefore
// carried a working second factor, which is the credential the exported data is
// protected BY.
//
// The check reads the REAL migrated schema rather than a list kept beside it,
// for exactly the reason the defect existed: a list beside the schema is a thing
// somebody has to remember.
func TestExportedCredentialColumnsAreRedacted(t *testing.T) {
	g := newMigratedSource(t)

	var unguarded []string
	for _, spec := range manifest {
		type col struct {
			Name string `gorm:"column:name"`
		}
		var cols []col
		if err := g.Raw("SELECT name FROM pragma_table_info(?)", spec.table).Scan(&cols).Error; err != nil {
			t.Fatalf("read columns of %s: %v", spec.table, err)
		}
		if len(cols) == 0 {
			t.Errorf("manifest exports %q but the migrated schema has no such table", spec.table)
			continue
		}
		for _, c := range cols {
			if !credentialColumn.MatchString(c.Name) {
				continue
			}
			qualified := spec.table + "." + c.Name
			if _, reviewed := publicByReview[qualified]; reviewed {
				continue
			}
			if _, redacted := redactors[spec.table][c.Name]; redacted {
				continue
			}
			unguarded = append(unguarded, qualified)
		}
	}
	sort.Strings(unguarded)
	for _, u := range unguarded {
		t.Errorf("%s is exported unredacted and its name says it is credential material — add a redactor, or add it to publicByReview with the reason it is safe", u)
	}
}

// TestRedactedColumnsStillExist fails when a redactor names a column that no
// longer exists, which means it silently stopped protecting anything.
//
// The failure mode is quiet in the other direction: a renamed or dropped column
// leaves its redactor in place looking like coverage, so the map reads as more
// complete than it is and the next reader trusts it.
func TestRedactedColumnsStillExist(t *testing.T) {
	g := newMigratedSource(t)

	for table, cols := range redactors {
		type col struct {
			Name string `gorm:"column:name"`
		}
		var actual []col
		if err := g.Raw("SELECT name FROM pragma_table_info(?)", table).Scan(&actual).Error; err != nil {
			t.Fatalf("read columns of %s: %v", table, err)
		}
		have := map[string]bool{}
		for _, c := range actual {
			have[c.Name] = true
		}
		for name := range cols {
			if !have[name] {
				t.Errorf("redactors[%q][%q] guards a column that does not exist — it protects nothing and reads as if it does", table, name)
			}
		}
	}
}

// TestPublicByReviewIsJustified keeps the allowlist honest: an entry with no
// reason is an entry nobody reviewed.
func TestPublicByReviewIsJustified(t *testing.T) {
	for qualified, reason := range publicByReview {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("publicByReview[%q] has no reason — say why it is safe or redact it", qualified)
		}
		if !strings.Contains(qualified, ".") {
			t.Errorf("publicByReview key %q must be table.column", qualified)
		}
	}
}
