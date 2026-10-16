// Package waffokit is a thin, project-agnostic integration layer over the
// official Waffo Pancake Go SDK (github.com/waffo-com/waffo-pancake-sdk-go).
//
// It packages the four things every host app re-invents when adopting Waffo:
//
//  1. Config from env (merchant id, RSA key, environment, store).
//  2. Authenticated checkout creation with a merchant-controlled BuyerIdentity
//     and a business ExternalID convention (Checkout/PortalLink).
//  3. A webhook HTTP handler: signature verification (SDK), eventId-level
//     idempotency (host-provided EventStore), typed dispatch (EventHandler).
//  4. A stub cashier for local development that signs real RSA webhooks and
//     feeds them through the exact same verify/dedupe/dispatch path, so stub
//     mode and production mode share one code path.
//
// The Kit is a concrete struct; hosts should wrap it in their own interface
// for test fakes. Waffo is the source of truth for billing state; handlers
// here are expected to be idempotent projections, never incremental math.
package waffokit

import (
	"errors"
	"fmt"
	"os"
	"strings"

	pancake "github.com/waffo-com/waffo-pancake-sdk-go"
)

// Mode selects the transport implementation.
type Mode string

const (
	// ModeLive talks to the real Waffo API.
	ModeLive Mode = "live"
	// ModeStub serves a local fake cashier and emits self-signed webhooks.
	ModeStub Mode = "stub"
)

// ErrNotConfigured is returned by NewFromEnv when live credentials are missing
// and WAFFO_STUB is not set.
var ErrNotConfigured = errors.New("waffokit: WAFFO_MERCHANT_ID/WAFFO_PRIVATE_KEY missing and WAFFO_STUB not set")

// Settings is the resolved configuration of a Kit.
type Settings struct {
	Mode        Mode
	Environment pancake.Environment
	MerchantID  string
	StoreID     string
	BaseURL     string // API override ("" = SDK default)
	PublicBase  string // our site base, used by stub cashier links & success_url default
	Currency    string // default checkout currency
	// WebhookPublicKey optionally overrides SDK built-in verify keys
	// (stub mode injects its generated key automatically).
	WebhookPublicKey string
}

// SettingsFromEnv reads WAFFO_* env vars. WAFFO_STUB=1 (or "true") yields
// stub mode without requiring credentials.
func SettingsFromEnv() (Settings, error) {
	s := Settings{
		Environment:      pancake.Environment(strings.ToLower(strings.TrimSpace(getenv("WAFFO_ENV", "test")))),
		MerchantID:       getenv("WAFFO_MERCHANT_ID", ""),
		StoreID:          getenv("WAFFO_STORE_ID", ""),
		BaseURL:          getenv("WAFFO_BASE_URL", ""),
		PublicBase:       getenv("WAFFO_PUBLIC_BASE", ""),
		Currency:         getenv("WAFFO_CURRENCY", "CNY"),
		WebhookPublicKey: getenv("WAFFO_WEBHOOK_PUBLIC_KEY", ""),
	}
	if stub := getenv("WAFFO_STUB", ""); stub == "1" || strings.EqualFold(stub, "true") {
		s.Mode = ModeStub
		return s, nil
	}
	if s.MerchantID == "" || getenv("WAFFO_PRIVATE_KEY", "") == "" {
		return Settings{}, ErrNotConfigured
	}
	s.Mode = ModeLive
	return s, nil
}

// Kit is the integration facade. Construct with New / NewFromEnv.
type Kit struct {
	settings   Settings
	client     *pancake.Client // nil in stub mode until handed live calls
	privateKey string          // live only
	stub       *Stub           // non-nil in stub mode
}

// New builds a Kit from Settings. In stub mode no network client is required.
func New(s Settings) (*Kit, error) {
	k := &Kit{settings: s}
	switch s.Mode {
	case ModeStub:
		st, err := newStub()
		if err != nil {
			return nil, fmt.Errorf("waffokit: stub key: %w", err)
		}
		k.stub = st
		return k, nil
	case ModeLive, "":
		pk := os.Getenv("WAFFO_PRIVATE_KEY")
		if pk == "" {
			return nil, ErrNotConfigured
		}
		cfg := pancake.Config{
			MerchantID:  s.MerchantID,
			PrivateKey:  pk,
			Environment: s.Environment,
		}
		if s.BaseURL != "" {
			cfg.BaseURL = s.BaseURL
		}
		if s.WebhookPublicKey != "" {
			cfg.WebhookPublicKey = pancake.WebhookPublicKeys{Shared: s.WebhookPublicKey}
		}
		c, err := pancake.New(cfg)
		if err != nil {
			return nil, fmt.Errorf("waffokit: pancake client: %w", err)
		}
		k.client, k.privateKey = c, pk
		return k, nil
	default:
		return nil, fmt.Errorf("waffokit: unknown mode %q", s.Mode)
	}
}

// NewFromEnv is New(SettingsFromEnv()).
func NewFromEnv() (*Kit, error) {
	s, err := SettingsFromEnv()
	if err != nil {
		return nil, err
	}
	return New(s)
}

// Settings exposes the resolved config (read-only copy).
func (k *Kit) Settings() Settings { return k.settings }

// IsStub reports whether the kit runs against the local fake cashier.
func (k *Kit) IsStub() bool { return k.settings.Mode == ModeStub }

func getenv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
