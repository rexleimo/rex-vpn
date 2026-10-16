# waffo-kit

Project-agnostic integration layer over the official Waffo Pancake Go SDK
(`github.com/waffo-com/waffo-pancake-sdk-go`). Packages what every host app
re-invents: env config, authenticated checkout, webhook verify+dedupe+dispatch,
customer-portal links, and a stub cashier that signs real RSA webhooks and
travels the exact same code path as production.

## Quick start

```go
k, err := waffokit.NewFromEnv() // WAFFO_MERCHANT_ID, WAFFO_PRIVATE_KEY, WAFFO_ENV, WAFFO_STORE_ID
res, err := k.CreateCheckout(ctx, waffokit.CheckoutRequest{
    ProductID: "PROD_...", BuyerIdentity: "acct:"+userID,
    ExternalID: waffokit.ExternalIDFor("uc_sub", orderID),
})
http.Redirect(w, r, res.CheckoutURL, 303)

h, _ := k.WebhookHandler(waffokit.WebhookDeps{Store: myEventStore, Handle: myProjector})
r.Handle("/webhooks/waffo", h) // signature verified, eventId deduped
```

## Stub mode (local dev / E2E)

`WAFFO_STUB=1` — no credentials needed. `k.StubHandler("/_waffo/stub")` serves a
fake cashier (pay / simulate renewal / simulate refund); confirmations are
RSA-signed with an ephemeral key and POSTed through the mounted
`WebhookHandler`, so verify/dedupe/dispatch are identical to live.

## Design rules

- Waffo is the billing source of truth; handlers are idempotent projections
  (resync from event facts like `currentPeriodEnd`), never incremental math.
- `BuyerIdentity` must come from your authenticated session, never browser input.
- Handler error → HTTP 5xx → Waffo redelivers with the same `eventId`
  (unprocessed events are NOT marked seen).
