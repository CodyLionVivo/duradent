// Package fakegateway simulates Culqi for local development without keys and
// for end-to-end tests. Tokens decide the outcome:
//
//	*declined*  → declined with a Spanish user message
//	*timeout*   → transport error (unknown outcome)
//	anything else → charge succeeds
//
// Never used outside APP_ENV=local (the config refuses it).
package fakegateway

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/orders/ports"
)

type Gateway struct {
	mu      sync.Mutex
	charges map[string]ports.Charge
}

func New() *Gateway { return &Gateway{charges: map[string]ports.Charge{}} }

var _ ports.PaymentGateway = (*Gateway)(nil)

func (g *Gateway) Name() string { return "fake" }

func (g *Gateway) Charge(_ context.Context, r ports.ChargeRequest) (ports.Charge, error) {
	src := "card"
	if strings.HasPrefix(r.SourceID, "ype_") {
		src = "yape"
	}
	c := ports.Charge{
		ID: "chr_test_" + id.New().String()[24:], AmountMinor: r.AmountMinor, Currency: r.Currency,
		OrderID: r.OrderID.String(), SourceType: src,
	}
	switch {
	case strings.Contains(r.SourceID, "timeout"):
		// Simulate "charged but the response was lost": the charge exists
		// and only the webhook can tell us.
		c.Succeeded = true
		g.store(c)
		return ports.Charge{}, errors.New("fake gateway: timeout")
	case strings.Contains(r.SourceID, "declined"):
		c.Code, c.UserMessage = "card_declined", "Tu tarjeta fue rechazada (simulación)."
	default:
		c.Succeeded = true
	}
	g.store(c)
	return c, nil
}

func (g *Gateway) store(c ports.Charge) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.charges[c.ID] = c
}

func (g *Gateway) GetCharge(_ context.Context, chargeID string) (ports.Charge, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	c, ok := g.charges[chargeID]
	if !ok {
		return ports.Charge{}, errors.New("fake gateway: unknown charge")
	}
	return c, nil
}

// Charges lists recorded charges (tests use it to simulate webhooks).
func (g *Gateway) Charges() []ports.Charge {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]ports.Charge, 0, len(g.charges))
	for _, c := range g.charges {
		out = append(out, c)
	}
	return out
}

// Refund succeeds for any known, succeeded charge.
func (g *Gateway) Refund(_ context.Context, r ports.RefundRequest) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	c, ok := g.charges[r.ChargeID]
	if !ok || !c.Succeeded || r.AmountMinor > c.AmountMinor {
		return "", errors.New("fake gateway: charge cannot be refunded")
	}
	return "ref_test_" + id.New().String()[24:], nil
}
