// Package culqi charges cards and Yape through Culqi's API v2.
//
// The browser tokenizes the card or Yape approval code with Culqi Checkout
// (public key); this adapter creates the charge server-side with the secret
// key. Docs: https://docs.culqi.com/es/documentacion/pagos-online/cargo-unico/cargos
package culqi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/money"
	"github.com/CodyLionVivo/duradent/services/api/internal/orders/ports"
)

const DefaultBaseURL = "https://api.culqi.com/v2"

type Client struct {
	baseURL   string
	secretKey string
	http      *http.Client
}

func New(secretKey, baseURL string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"), secretKey: secretKey,
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

var _ ports.PaymentGateway = (*Client)(nil)

func (c *Client) Name() string { return "culqi" }

type chargeBody struct {
	Amount       int64             `json:"amount"`
	CurrencyCode string            `json:"currency_code"`
	Email        string            `json:"email"`
	SourceID     string            `json:"source_id"`
	Description  string            `json:"description,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	Antifraud    map[string]any    `json:"antifraud_details,omitempty"`
}

// charge is the subset of Culqi's charge object we use. Culqi reports the
// result either in response_code or in outcome.type ("venta_exitosa").
type charge struct {
	Object       string            `json:"object"`
	ID           string            `json:"id"`
	Amount       int64             `json:"amount"`
	Currency     string            `json:"currency_code"`
	CurrencyAlt  string            `json:"currency"`
	ResponseCode string            `json:"response_code"`
	UserMessage  string            `json:"user_message"`
	ActionCode   string            `json:"action_code"` // "REVIEW": issuer requires 3-D Secure
	Metadata     map[string]string `json:"metadata"`
	Outcome      struct {
		Type        string `json:"type"`
		Code        string `json:"code"`
		UserMessage string `json:"user_message"`
	} `json:"outcome"`
	Source struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	} `json:"source"`
}

type culqiError struct {
	Object      string `json:"object"`
	Type        string `json:"type"`
	Code        string `json:"code"`
	DeclineCode string `json:"decline_code"`
	UserMessage string `json:"user_message"`
	MerchantMsg string `json:"merchant_message"`
	ChargeID    string `json:"charge_id"`
}

func (ch charge) toPort() ports.Charge {
	cur := ch.Currency
	if cur == "" {
		cur = ch.CurrencyAlt
	}
	succeeded := ch.Object == "charge" && ch.ActionCode == "" &&
		(ch.ResponseCode == "venta_exitosa" || ch.Outcome.Type == "venta_exitosa")
	out := ports.Charge{
		ID: ch.ID, Succeeded: succeeded, AmountMinor: ch.Amount, Currency: money.Currency(strings.ToUpper(cur)),
		OrderID: ch.Metadata["order_id"], SourceType: sourceType(ch.Source.Type, ch.Source.ID),
	}
	if !succeeded {
		out.Code = firstNonEmpty(ch.Outcome.Code, ch.ResponseCode, ch.ActionCode, "declined")
		out.UserMessage = firstNonEmpty(ch.Outcome.UserMessage, ch.UserMessage)
		if ch.ActionCode == "REVIEW" {
			out.Code = "3ds_required"
		}
	}
	return out
}

func sourceType(t, sourceID string) string {
	if strings.HasPrefix(sourceID, "ype_") || strings.EqualFold(t, "yape") {
		return "yape"
	}
	return "card"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func (c *Client) do(ctx context.Context, method, path string, body any) (int, []byte, error) {
	var buf io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		buf = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, buf)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.secretKey)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("culqi %s %s: %w", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	return res.StatusCode, raw, err
}

func (c *Client) Charge(ctx context.Context, r ports.ChargeRequest) (ports.Charge, error) {
	status, raw, err := c.do(ctx, http.MethodPost, "/charges", chargeBody{
		Amount: r.AmountMinor, CurrencyCode: string(r.Currency), Email: r.Email, SourceID: r.SourceID,
		Description: truncate(r.Description, 80),
		Metadata:    map[string]string{"order_id": r.OrderID.String(), "order_number": r.OrderNumber},
	})
	if err != nil {
		return ports.Charge{}, err
	}
	switch {
	case status >= 200 && status < 300:
		var ch charge
		if err := json.Unmarshal(raw, &ch); err != nil {
			return ports.Charge{}, fmt.Errorf("culqi charge response: %w", err)
		}
		return ch.toPort(), nil
	case status >= 400 && status < 500:
		// Declines and invalid tokens: a definitive "no", safe to show.
		var e culqiError
		if err := json.Unmarshal(raw, &e); err != nil || e.Object != "error" {
			return ports.Charge{}, fmt.Errorf("culqi charge: unexpected %d response", status)
		}
		return ports.Charge{
			ID: e.ChargeID, Succeeded: false, AmountMinor: r.AmountMinor, Currency: r.Currency,
			Code: firstNonEmpty(e.DeclineCode, e.Code, e.Type), UserMessage: e.UserMessage, SourceType: sourceType("", r.SourceID),
		}, nil
	default:
		return ports.Charge{}, fmt.Errorf("culqi charge: status %d", status)
	}
}

func (c *Client) GetCharge(ctx context.Context, chargeID string) (ports.Charge, error) {
	status, raw, err := c.do(ctx, http.MethodGet, "/charges/"+chargeID, nil)
	if err != nil {
		return ports.Charge{}, err
	}
	if status != http.StatusOK {
		return ports.Charge{}, fmt.Errorf("culqi get charge %s: status %d", chargeID, status)
	}
	var ch charge
	if err := json.Unmarshal(raw, &ch); err != nil {
		return ports.Charge{}, fmt.Errorf("culqi charge response: %w", err)
	}
	return ch.toPort(), nil
}

// Refund creates a refund (POST /v2/refunds). Culqi needs a reason from a
// fixed list; ours are always requested by the customer or by Duradent.
func (c *Client) Refund(ctx context.Context, r ports.RefundRequest) (string, error) {
	status, raw, err := c.do(ctx, http.MethodPost, "/refunds", map[string]any{
		"amount": r.AmountMinor, "charge_id": r.ChargeID, "reason": "solicitud_comprador",
	})
	if err != nil {
		return "", err
	}
	if status < 200 || status >= 300 {
		var e culqiError
		_ = json.Unmarshal(raw, &e)
		return "", fmt.Errorf("culqi refund %s: status %d %s", r.ChargeID, status, firstNonEmpty(e.MerchantMsg, e.UserMessage, e.Code))
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.ID == "" {
		return "", fmt.Errorf("culqi refund response: %w", err)
	}
	return out.ID, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// WebhookChargeID extracts the charge id from a Culqi webhook body. Culqi
// sends an event whose "data" is the charge (as an object or a JSON string).
// The id is only a pointer: callers must re-read the charge from the API.
func WebhookChargeID(body []byte) (string, error) {
	var ev struct {
		Object string          `json:"object"`
		Type   string          `json:"type"`
		Data   json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		return "", fmt.Errorf("webhook body: %w", err)
	}
	data := ev.Data
	var asString string
	if json.Unmarshal(data, &asString) == nil {
		data = json.RawMessage(asString)
	}
	var ch struct {
		Object string `json:"object"`
		ID     string `json:"id"`
	}
	if err := json.Unmarshal(data, &ch); err != nil {
		return "", fmt.Errorf("webhook data: %w", err)
	}
	if !strings.HasPrefix(ch.ID, "chr_") {
		return "", nil // not a charge event: ignore
	}
	return ch.ID, nil
}
