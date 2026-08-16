package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
)

// TestLocalTenantAdmitsWithoutCredential proves the self-hosted middleware puts
// the fixed workspace exactly where TenantFrom reads it, with no Authorization
// header present — the whole point being that downstream tools cannot tell a
// local request from a token-resolved one.
func TestLocalTenantAdmitsWithoutCredential(t *testing.T) {
	want := tenant.Tenant{TeamID: "team-local", UserID: "user-local", Role: tenant.RoleAdmin}

	var got tenant.Tenant
	var ok bool
	h := LocalTenant(want)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, ok = TenantFrom(r.Context())
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/mcp", nil))

	if !ok {
		t.Fatal("no tenant on context; local mode must admit an unauthenticated request")
	}
	if got != want {
		t.Errorf("tenant = %+v, want %+v", got, want)
	}
}

// TestLocalTenantIgnoresInboundCredential confirms a stray bearer token cannot
// steer local mode: the injected workspace wins regardless of what the caller
// sends, so a leftover token in an agent's config resolves to the local
// workspace rather than failing or selecting another one.
func TestLocalTenantIgnoresInboundCredential(t *testing.T) {
	want := tenant.Tenant{TeamID: "team-local", UserID: "user-local", Role: tenant.RoleAdmin}

	var got tenant.Tenant
	h := LocalTenant(want)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, _ = TenantFrom(r.Context())
	}))

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer someone-elses-token")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got != want {
		t.Errorf("tenant = %+v, want %+v", got, want)
	}
}
