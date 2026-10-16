package waffokit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	pancake "github.com/waffo-com/waffo-pancake-sdk-go"
)

// Event type constants (mirrors the Waffo event catalog; values are the wire
// strings, so compare with Event.Type directly).
const (
	EventOrderCompleted                  = "order.completed"
	EventSubscriptionActivated           = "subscription.activated"
	EventSubscriptionPaymentSucceeded    = "subscription.payment_succeeded"
	EventSubscriptionRenewed             = "subscription.renewed"
	EventSubscriptionRecovered           = "subscription.recovered"
	EventSubscriptionPlanChanged         = "subscription.plan_changed"
	EventSubscriptionPlanChangeScheduled = "subscription.plan_change_scheduled"
	EventSubscriptionPlanChangeFailed    = "subscription.plan_change_failed"
	EventSubscriptionCanceling           = "subscription.canceling"
	EventSubscriptionUncanceled          = "subscription.uncanceled"
	EventSubscriptionCanceled            = "subscription.canceled"
	EventSubscriptionPastDue             = "subscription.past_due"
	EventRefundSucceeded                 = "refund.succeeded"
	EventRefundFailed                    = "refund.failed"
)

// MaxWebhookBody bounds the accepted webhook payload size.
const MaxWebhookBody = 1 << 20 // 1 MiB

// Event is a verified webhook event with decoded data.
type Event struct {
	ID        string
	EventID   string // stable business id: dedupe key
	Type      string
	Timestamp string
	Mode      string
	Data      *pancake.WebhookEventData
}

// EventStore provides eventId-level idempotency. Hosts implement it on their
// own database (a unique index on event_id is enough).
//
// Contract: Seen reports whether an event was already processed. MarkSeen is
// called only after a successful handler return, so a handler error keeps the
// event eligible for Waffo's redelivery. Concurrent duplicates are tolerated
// by design: handlers are idempotent projections (resync from event facts),
// never incremental arithmetic.
type EventStore interface {
	Seen(eventID string) (bool, error)
	MarkSeen(eventID, eventType string) error
}

// EventHandler receives verified, non-duplicate events. Returning an error
// makes the HTTP handler answer 5xx so Waffo retries the delivery.
type EventHandler interface {
	HandleWaffoEvent(ctx context.Context, ev *Event) error
}

// WebhookDeps wires the HTTP handler.
type WebhookDeps struct {
	Store           EventStore
	Handle          func(ctx context.Context, ev *Event) error // convenience; mutually exclusive with EventHandler
	Dispatcher      EventHandler                               // takes precedence over Handle
	SignatureHeader string                                     // default "X-Waffo-Signature"
}

// WebhookHandler returns an http.Handler that verifies the signature, decodes
// the event, dedupes by eventId and dispatches. Responses are JSON; Waffo only
// requires 2xx as ack.
func (k *Kit) WebhookHandler(deps WebhookDeps) (http.Handler, error) {
	if deps.Store == nil {
		return nil, fmt.Errorf("waffokit: WebhookDeps.Store required")
	}
	fn := deps.Handle
	if deps.Dispatcher != nil {
		fn = deps.Dispatcher.HandleWaffoEvent
	}
	if fn == nil {
		return nil, fmt.Errorf("waffokit: WebhookDeps needs Handle or Dispatcher")
	}
	hdr := deps.SignatureHeader
	if hdr == "" {
		hdr = "X-Waffo-Signature"
	}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method"}`, http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, MaxWebhookBody+1))
		if err != nil || len(body) > MaxWebhookBody {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "payload unreadable"})
			return
		}
		ev, err := k.verifyEvent(r.Header.Get(hdr), body)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "signature invalid"})
			return
		}
		status, err := k.processEvent(r.Context(), ev, fn, deps.Store)
		if err != nil {
			// 5xx → Waffo retries with the same eventId/timestamp.
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error(), "status": status})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"received": true, "status": status, "eventType": ev.Type})
	})
	if k.stub != nil {
		k.stub.setRelay(h) // stub confirmations travel the full verify path
	}
	return h, nil
}

// verifyEvent runs the SDK signature check (shared keys in stub mode).
func (k *Kit) verifyEvent(sigHeader string, body []byte) (*Event, error) {
	opts := &pancake.VerifyWebhookOptions{}
	if k.stub != nil {
		opts.PublicKey = k.stub.publicKeyPEM()
	} else if k.settings.WebhookPublicKey != "" {
		opts.PublicKey = k.settings.WebhookPublicKey
	}
	raw, err := pancake.VerifyWebhook(string(body), sigHeader, opts)
	if err != nil {
		return nil, err
	}
	var data pancake.WebhookEventData
	if len(raw.Data) > 0 {
		if err := json.Unmarshal(raw.Data, &data); err != nil {
			return nil, fmt.Errorf("waffokit: event data: %w", err)
		}
	}
	return &Event{ID: raw.ID, EventID: raw.EventID, Type: raw.EventType,
		Timestamp: raw.Timestamp, Mode: string(raw.Mode), Data: &data}, nil
}

// processEvent is the single dedupe+dispatch core used by the HTTP handler
// and the stub relay alike.
func (k *Kit) processEvent(ctx context.Context, ev *Event, fn func(context.Context, *Event) error, store EventStore) (string, error) {
	if ev.EventID == "" {
		return "invalid", fmt.Errorf("waffokit: event without eventId")
	}
	seen, err := store.Seen(ev.EventID)
	if err != nil {
		return "store", err
	}
	if seen {
		return "duplicate", nil
	}
	if err := fn(ctx, ev); err != nil {
		return "handler", err
	}
	if err := store.MarkSeen(ev.EventID, ev.Type); err != nil {
		return "store", err
	}
	return "processed", nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
