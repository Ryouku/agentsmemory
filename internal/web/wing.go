package web

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/importer"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/web/views"
	"github.com/atvirokodosprendimai/agentsmemory/internal/wingbundle"
)

// maxWingUploadBytes caps an uploaded bundle. It is deliberately smaller than the
// 256 MiB the token-authenticated POST /import accepts: this path is a browser
// form, where a multi-hundred-megabyte upload would time out long before it
// finished, and anyone with a palace that size is using the CLI or the API.
const maxWingUploadBytes = 64 << 20 // 64 MiB

// WingTransfer is the slice of the palace the wing transfer handlers need: the
// read side an export streams from, and the write side an upload is filed
// through. Declared here at the consumer so the dashboard depends on these
// methods rather than on the whole memory service; *palace.Service satisfies it.
type WingTransfer interface {
	wingbundle.Source
	importer.Drawers
}

// getWingExport streams one wing as a portable bundle file.
//
// Access is membership at any role, matching the whole-workspace export in
// export.go and for the same reason: a member already reads this wing's memories
// over MCP, so handing them over as a file adds no exposure.
//
// The bundle is built to a temp file IN FULL before a single response header is
// written. Streaming straight to the response would be cheaper, but an error
// halfway through a wing would leave the browser with a truncated file that
// looks complete — and a bundle whose tail is missing imports silently, losing
// exactly the memories nobody notices are gone.
func (s *Server) getWingExport(w http.ResponseWriter, r *http.Request) {
	_, teamID, _, ok := s.membership(w, r)
	if !ok {
		return
	}
	wing := r.URL.Query().Get("wing")
	if wing == "" {
		http.Error(w, "name the wing to export: ?wing=<name>", http.StatusBadRequest)
		return
	}

	f, err := os.CreateTemp("", "wing-bundle-*.ndjson")
	if err != nil {
		http.Error(w, "could not build the bundle", http.StatusInternalServerError)
		return
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}()

	if _, err := wingbundle.Export(r.Context(), s.wings, teamID, wing, f); err != nil {
		// An unknown wing is the caller's mistake, not the server's, and the
		// error already names the wings that do exist.
		if errors.Is(err, wingbundle.ErrUnknownWing) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, "could not build the bundle", http.StatusInternalServerError)
		return
	}

	info, err := f.Stat()
	if err != nil {
		http.Error(w, "could not read the bundle", http.StatusInternalServerError)
		return
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		http.Error(w, "could not read the bundle", http.StatusInternalServerError)
		return
	}

	// no-store because the bundle is the workspace's memories in plain text and
	// must not sit in a shared or proxy cache.
	filename := "wing-" + wing + "-" + time.Now().UTC().Format("20060102") + ".ndjson"
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.Copy(w, f)
}

// postWingImport files an uploaded bundle into the wing named on the form.
//
// It takes the writer/admin bar rather than plain membership: this writes
// memories into the workspace, which is the same governance class as editing a
// shared skill or pushing a wing to another tenant.
//
// The response re-renders the project page with a result flash rather than
// redirecting, so the counts stay in front of the person who just uploaded.
// Re-submitting the form is safe: every record id is deterministic, so a repeat
// import upserts rather than duplicating.
func (s *Server) postWingImport(w http.ResponseWriter, r *http.Request) {
	u, teamID, role, ok := s.membership(w, r)
	if !ok {
		return
	}
	if !tenant.CanWrite(role) {
		http.Error(w, "importing a wing needs the writer or admin role", http.StatusForbidden)
		return
	}

	// Bound the upload before parsing it. The 32 KiB argument is only how much of
	// the multipart form is held in memory; the rest spills to a temp file that
	// ParseMultipartForm cleans up when the request ends.
	r.Body = http.MaxBytesReader(w, r.Body, maxWingUploadBytes)
	if err := r.ParseMultipartForm(32 << 10); err != nil {
		s.renderWingFlash(w, r, u, teamID, role, "error",
			"That upload was rejected — a bundle must be under 64 MB here. For a larger palace use `agentsmemory wing import` or POST /import with your API key.")
		return
	}

	// Validate the destination before touching the file: the value becomes a
	// stored wing label, so it passes the same check as any agent-supplied name.
	wing, err := palace.SanitizeName(r.FormValue("wing"), "wing")
	if err != nil {
		s.renderWingFlash(w, r, u, teamID, role, "error",
			"Name the wing to import into — letters, numbers, spaces, dots, hyphens and underscores only.")
		return
	}

	file, _, err := r.FormFile("bundle")
	if err != nil {
		s.renderWingFlash(w, r, u, teamID, role, "error", "Choose a bundle file to import.")
		return
	}
	defer file.Close()

	// recompute=true: a browser upload is a single-shot import, so the derived
	// graph is rebuilt once here rather than left stale until something else runs.
	res := importer.Ingest(r.Context(), s.wings, teamID, wing, file, true)
	if res.Error != "" {
		s.renderWingFlash(w, r, u, teamID, role, "error", "Import failed: "+res.Error)
		return
	}

	msg := fmt.Sprintf("Imported %d drawers and %d closets into %s.", res.Drawers, res.Closets, wing)
	if res.Tunnels > 0 {
		msg = fmt.Sprintf("Imported %d drawers, %d closets and %d tunnels into %s.",
			res.Drawers, res.Closets, res.Tunnels, wing)
	}
	// Say that search lags the import, or the wing looks broken for a few minutes:
	// rows land immediately, the background worker embeds them afterwards.
	if res.Pending > 0 {
		msg += fmt.Sprintf(" %d memories are being indexed and will become searchable shortly.", res.Pending)
	}
	s.renderWingFlash(w, r, u, teamID, role, "success", msg)
}

// renderWingFlash re-renders the project page carrying one result message. It
// exists so every exit from the import handler returns the same full page with
// the outcome attached, instead of a bare error string on a blank screen.
func (s *Server) renderWingFlash(w http.ResponseWriter, r *http.Request, u tenant.User, teamID string, role tenant.Role, kind, msg string) {
	s.renderProjectPage(w, r, u, teamID, role, views.FlashVM{Kind: kind, Message: msg})
}

// buildWingTransferData shapes the wing transfer card: the wings available to
// download and whether the viewer may upload one. A wing-name lookup failure
// degrades the card to its import half rather than failing the whole page —
// the same per-part degradation the merge and members sections use.
func (s *Server) buildWingTransferData(r *http.Request, teamID string, role tenant.Role) views.WingTransferVM {
	d := views.WingTransferVM{
		TeamID:    teamID,
		CanImport: tenant.CanWrite(role),
	}
	if names, err := s.merges.WingNames(r.Context(), teamID); err == nil {
		d.Wings = names
	}
	return d
}
