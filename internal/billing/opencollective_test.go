package billing

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
)

const (
	testOCMonthlyURL = "https://opencollective.com/it-uoga/projects/ai-agents-memory/contribute/pro-monthly-104934/checkout"
	testOCAnnualURL  = "https://opencollective.com/it-uoga/projects/ai-agents-memory/contribute/pro-yearly-104935/checkout"
	testOCProjectURL = "https://opencollective.com/it-uoga/projects/ai-agents-memory"
)

// ocTestEnv builds a billing Service wired to the OpenCollective provider over
// the same migrated in-memory DB as the Stripe tests (see newTestEnv), swapping
// the provider seams onto its Service. The OC provider needs no credentials, so
// the only config is the plan->checkout-URL map and the project URL.
func ocTestEnv(t *testing.T) (*Service, *tenant.Repo, string) {
	t.Helper()
	svc, _, tenants, _, teamID := newTestEnv(t)
	oc := newOpenCollectiveProvider(Config{
		PriceByPlanCode: map[string]string{
			"pro_monthly": testOCMonthlyURL,
			"pro_annual":  testOCAnnualURL,
		},
		OpenCollectiveProjectURL: testOCProjectURL,
	})
	svc.checkout, svc.webhook, svc.portal = oc, oc, oc
	return svc, tenants, teamID
}

func TestNewOpenCollectiveProvider_NilWithoutURLs(t *testing.T) {
	if p := newOpenCollectiveProvider(Config{}); p != nil {
		t.Fatalf("expected nil provider with no checkout URLs, got %+v", p)
	}
}

func TestOpenCollectiveStartCheckout_ReturnsStaticURL(t *testing.T) {
	svc, _, teamID := ocTestEnv(t)
	if !svc.Enabled() {
		t.Fatal("expected OC provider to be enabled with checkout URLs configured")
	}
	url, err := svc.StartCheckout(context.Background(), CheckoutRequest{
		TeamID: teamID, PlanCode: "pro_monthly", CustomerEmail: "a@b.co",
		SuccessURL: "https://app/ok", CancelURL: "https://app/no",
	})
	if err != nil {
		t.Fatalf("StartCheckout: %v", err)
	}
	// The checkout IS the static contribution page — no session to create.
	if url != testOCMonthlyURL {
		t.Fatalf("url = %q, want %q", url, testOCMonthlyURL)
	}
}

func TestOpenCollectiveStartCheckout_UnknownPlan(t *testing.T) {
	svc, _, teamID := ocTestEnv(t)
	if _, err := svc.StartCheckout(context.Background(), CheckoutRequest{
		TeamID: teamID, PlanCode: "enterprise",
	}); !errors.Is(err, ErrUnknownPlan) {
		t.Fatalf("expected ErrUnknownPlan, got %v", err)
	}
}

func TestOpenCollectiveWebhook_FailsClosed(t *testing.T) {
	svc, _, teamID := ocTestEnv(t)
	// OpenCollective has no signed webhook channel: even a well-formed payload with
	// any headers must be rejected, so nothing can ever be activated from an
	// unsigned event.
	payload := []byte(`{"type":"donation.created"}`)
	if err := svc.HandleWebhook(context.Background(), payload, http.Header{}); err == nil {
		t.Fatal("expected fail-closed rejection: OpenCollective sends no signed webhook")
	}
	if got := teamPlanID(t, svc.subs.db, teamID); got != tenant.FreePlanID {
		t.Fatalf("plan changed via opencollective webhook: %q", got)
	}
}

func TestOpenCollectiveManageURL_OrderIDOnly(t *testing.T) {
	svc, _, teamID := ocTestEnv(t)
	// An OC-backed subscription records the order id in StripeSubscriptionID and
	// has no customer id — the relationship must still count for ManageURL.
	if err := svc.subs.Upsert(context.Background(), Subscription{
		TeamID: teamID, PlanID: "pro", Status: "active",
		StripeSubscriptionID: "order_oc_123",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	url, err := svc.ManageURL(context.Background(), teamID, "https://app/back")
	if err != nil {
		t.Fatalf("ManageURL: %v", err)
	}
	// No portal API exists: the stable project page is the manage/cancel surface.
	if url != testOCProjectURL {
		t.Fatalf("url = %q, want %q", url, testOCProjectURL)
	}
}

func TestOpenCollectiveManageURL_NoSubscription(t *testing.T) {
	svc, _, teamID := ocTestEnv(t)
	if _, err := svc.ManageURL(context.Background(), teamID, "https://app/back"); !errors.Is(err, ErrNoSubscription) {
		t.Fatalf("expected ErrNoSubscription, got %v", err)
	}
}

func TestOpenCollectivePortal_UnconfiguredProjectURL(t *testing.T) {
	oc := &openCollectiveProvider{
		checkoutURLs: map[string]string{"pro_monthly": testOCMonthlyURL},
	}
	if _, err := oc.createPortalSession(context.Background(), "", ""); err == nil {
		t.Fatal("expected error when OPENCOLLECTIVE_PROJECT_URL is unset")
	}
}
