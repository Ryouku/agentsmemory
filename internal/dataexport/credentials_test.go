package dataexport

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
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

// omittedByReview lists tables the archive deliberately does not carry, each
// with the reason. It is the mirror of publicByReview: that one justifies
// including something credential-shaped, this one justifies excluding something
// the subject might expect.
var omittedByReview = map[string]string{
	// Second factors. Exporting them would hand over the credential the account
	// is protected BY — the same reasoning that redacts users.totp_secret.
	"totp_recovery_codes":  "one-time second-factor codes; exporting them defeats the second factor",
	"webauthn_credentials": "passkey credentials; the subject's authenticators, not their data",
	// Schema bookkeeping and the global playbook, neither workspace data.
	"goose_db_version": "migration bookkeeping, meaningless outside this database",
	"skillset":         "global default playbook, identical for every workspace",
}

// TestArchiveCarriesOnlyTablesItFills reads the archive that actually ships and
// fails when it contains a table the export never fills.
//
// This is the credentials test's other direction, and it hides the worse
// failure. replaySchema used to copy the DDL of every table in the source while
// copyRows fills only the manifest, so any table added by a later migration and
// never added to the manifest arrived — empty. A subject running COUNT(*) on it
// reads 0 as "I had none", which is a false answer given under a data-protection
// request rather than a missing feature.
//
// Four tables were in that state and nothing failed: drawer_anchors,
// search_events, totp_recovery_codes and webauthn_credentials. The last two must
// never be exported at all, so shipping their empty shells was doubly wrong — it
// advertised second factors the archive is right not to contain.
//
// The check reads the produced file rather than the manifest, because the
// manifest is the thing that was wrong.
func TestArchiveCarriesOnlyTablesItFills(t *testing.T) {
	ctx := context.Background()
	src := newMigratedSource(t)
	const team, user = "team-a", "user-a"
	exec(t, src, `INSERT INTO teams (id,name,slug,created_at,kind) VALUES (?,?,?,?,?)`,
		team, "Alpha", "alpha", "2026-01-01T00:00:00Z", "personal")
	exec(t, src, `INSERT INTO users (id,email,password_hash,display_name,created_at) VALUES (?,?,?,?,?)`,
		user, "a@example.com", "hash", "A", "2026-01-01T00:00:00Z")
	exec(t, src, `INSERT INTO memberships (id,team_id,user_id,role,created_at) VALUES (?,?,?,?,?)`,
		"m-a", team, user, "admin", "2026-01-01T00:00:00Z")

	path, cleanup, err := New(src).BuildTeamArchive(ctx, team, user)
	if err != nil {
		t.Fatalf("build archive: %v", err)
	}
	defer func() { _ = cleanup() }()

	arc, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	type row struct {
		Name string `gorm:"column:name"`
	}
	var got []row
	if err := arc.Raw(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`).
		Scan(&got).Error; err != nil {
		t.Fatalf("read archive schema: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("the archive has no tables at all — this check has stopped checking anything")
	}

	filled := map[string]bool{}
	for _, spec := range manifest {
		filled[spec.table] = true
	}
	for _, tb := range got {
		if !filled[tb.Name] {
			t.Errorf("the archive contains table %q, which the export never fills — an empty "+
				"table reads as \"you had none\" rather than \"this was not exported\"", tb.Name)
		}
	}

	// And the sensitive ones must be absent outright, not present-and-empty.
	present := map[string]bool{}
	for _, tb := range got {
		present[tb.Name] = true
	}
	for table, why := range omittedByReview {
		if present[table] {
			t.Errorf("the archive contains %q, which is excluded because %s — its shell should "+
				"not ship either", table, why)
		}
	}
}
