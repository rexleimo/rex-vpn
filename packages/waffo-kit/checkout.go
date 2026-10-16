package waffokit

import (
	"context"
	"fmt"
	"strings"

	pancake "github.com/waffo-com/waffo-pancake-sdk-go"
)

// CheckoutRequest describes an authenticated checkout for a signed-in
// customer of the host app.
type CheckoutRequest struct {
	// ProductID is the Waffo subscription product (PROD_...). Hosts keep the
	// plan→product mapping; the kit stays generic.
	ProductID string
	// BuyerIdentity is the host's stable account id (e.g. "acct:<uuid>"). It
	// binds the order to the account regardless of the email typed on the
	// cashier form — never take it from browser input.
	BuyerIdentity string
	// ExternalID is the merchant business id inherited by orders/payments/
	// refunds (max 128 chars). Convention: "<entity>:<uuid>", see ExternalIDFor.
	ExternalID string
	// BuyerEmail only pre-fills the cashier form; identity is BuyerIdentity.
	BuyerEmail string
	Currency   string // defaults to Settings.Currency
	SuccessURL string
	Language   string // BCP-47, e.g. "zh-CN"; empty = provider infer
	Metadata   map[string]string
	// StubHint carries display info for the fake cashier (ignored in live).
	StubHint StubHint
}

// StubHint is display/behaviour data for the stub cashier.
type StubHint struct {
	PlanName   string
	Price      string // display only, e.g. "¥29/月"
	PeriodDays int    // period length for simulated events (default 30)
}

// CheckoutResult is the outcome for either mode.
type CheckoutResult struct {
	CheckoutURL string
	SessionID   string
}

// CreateCheckout creates a hosted checkout session (live) or a stub cashier
// link (stub). Both return a URL the browser should navigate to.
func (k *Kit) CreateCheckout(ctx context.Context, req CheckoutRequest) (*CheckoutResult, error) {
	if strings.TrimSpace(req.BuyerIdentity) == "" {
		return nil, fmt.Errorf("waffokit: BuyerIdentity required")
	}
	if req.ExternalID != "" && len(req.ExternalID) > 128 {
		return nil, fmt.Errorf("waffokit: ExternalID exceeds 128 chars")
	}
	currency := req.Currency
	if currency == "" {
		currency = k.settings.Currency
	}
	if k.IsStub() {
		return k.createStubCheckout(req, currency)
	}
	if k.client == nil {
		return nil, ErrNotConfigured
	}
	res, err := k.client.Checkout.Authenticated.Create(ctx, pancake.AuthenticatedCheckoutParams{
		CreateCheckoutSessionParams: pancake.CreateCheckoutSessionParams{
			ProductID:               req.ProductID,
			Currency:                currency,
			OrderMerchantExternalID: optString(req.ExternalID),
			BuyerEmail:              optString(req.BuyerEmail),
			SuccessURL:              optString(req.SuccessURL),
			Metadata:                req.Metadata,
			Language:                cashierLanguage(req.Language),
		},
		BuyerIdentity: req.BuyerIdentity,
	})
	if err != nil {
		return nil, fmt.Errorf("waffokit: checkout: %w", err)
	}
	return &CheckoutResult{CheckoutURL: res.CheckoutURL, SessionID: res.SessionID}, nil
}

// PortalLink mints a customer-portal URL for the identity ("manage
// subscription" entry). The URL embeds a bearer token fragment: issue per
// click, redirect immediately, never log or cache the result.
func (k *Kit) PortalLink(ctx context.Context, buyerIdentity string) (string, error) {
	if k.IsStub() {
		// No portal in local mode: point back at the host's billing page.
		base := strings.TrimRight(k.settings.PublicBase, "/")
		return base + "/billing?stub_portal=1", nil
	}
	if k.client == nil {
		return "", ErrNotConfigured
	}
	if k.settings.StoreID == "" {
		return "", fmt.Errorf("waffokit: WAFFO_STORE_ID required for portal links")
	}
	res, err := k.client.Auth.CreateCustomerPortalLink(ctx, pancake.CreateCustomerPortalLinkParams{
		StoreID:       k.settings.StoreID,
		BuyerIdentity: buyerIdentity,
	})
	if err != nil {
		return "", fmt.Errorf("waffokit: portal link: %w", err)
	}
	return res.PortalURL, nil
}

// ExternalIDFor builds the conventional business id ("<entity>:<uuid>"),
// truncated to the 128-char wire limit.
func ExternalIDFor(entity, id string) string {
	s := entity + ":" + id
	if len(s) > 128 {
		s = s[:128]
	}
	return s
}

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func cashierLanguage(tag string) *pancake.CashierLanguage {
	if tag == "" {
		return nil
	}
	l := pancake.CashierLanguage(tag)
	return &l
}
