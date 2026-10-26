package culqi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/money"
	"github.com/CodyLionVivo/duradent/services/api/internal/orders/ports"
)

func fakeCulqi(t *testing.T, handler func(w http.ResponseWriter, body map[string]any)) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk_test_123" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		if r.Method == http.MethodGet {
			body = map[string]any{"get": strings.TrimPrefix(r.URL.Path, "/charges/")}
		}
		handler(w, body)
	}))
	t.Cleanup(srv.Close)
	return New("sk_test_123", srv.URL)
}

func request() ports.ChargeRequest {
	return ports.ChargeRequest{AmountMinor: 21240, Currency: money.PEN, Email: "a@b.pe", SourceID: "tkn_test_abc",
		Description: "Pedido DD-000001", OrderID: id.MustParse("01a176ab-3866-7000-970b-6211c64669fb"), OrderNumber: "DD-000001"}
}

func TestChargeSucceeded(t *testing.T) {
	var sent map[string]any
	c := fakeCulqi(t, func(w http.ResponseWriter, body map[string]any) {
		sent = body
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"object":"charge","id":"chr_test_1","amount":21240,"currency_code":"PEN","response_code":"venta_exitosa",
			"source":{"object":"token","id":"tkn_test_abc","type":"card"},"metadata":{"order_id":"01a176ab-3866-7000-970b-6211c64669fb"}}`))
	})
	got, err := c.Charge(t.Context(), request())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Succeeded || got.ID != "chr_test_1" || got.AmountMinor != 21240 || got.SourceType != "card" || got.OrderID == "" {
		t.Fatalf("charge: %+v", got)
	}
	if sent["amount"].(float64) != 21240 || sent["source_id"] != "tkn_test_abc" || sent["metadata"].(map[string]any)["order_number"] != "DD-000001" {
		t.Fatalf("request body: %+v", sent)
	}
}

func TestChargeOutcomeStyleAndYape(t *testing.T) {
	c := fakeCulqi(t, func(w http.ResponseWriter, _ map[string]any) {
		_, _ = w.Write([]byte(`{"object":"charge","id":"chr_test_2","amount":500,"currency_code":"PEN",
			"outcome":{"type":"venta_exitosa","code":"AUT0000"},"source":{"id":"ype_test_x"}}`))
	})
	got, err := c.Charge(t.Context(), request())
	if err != nil || !got.Succeeded || got.SourceType != "yape" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestChargeDeclinedIsNotAnError(t *testing.T) {
	c := fakeCulqi(t, func(w http.ResponseWriter, _ map[string]any) {
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"object":"error","type":"card_error","charge_id":"chr_test_3","code":"card_declined",
			"decline_code":"insufficient_funds","user_message":"Su tarjeta no tiene fondos suficientes."}`))
	})
	got, err := c.Charge(t.Context(), request())
	if err != nil {
		t.Fatal(err)
	}
	if got.Succeeded || got.Code != "insufficient_funds" || !strings.Contains(got.UserMessage, "fondos") {
		t.Fatalf("decline: %+v", got)
	}
}

func TestChargeRequiring3DSIsNotPaid(t *testing.T) {
	c := fakeCulqi(t, func(w http.ResponseWriter, _ map[string]any) {
		_, _ = w.Write([]byte(`{"object":"charge","id":"chr_test_4","action_code":"REVIEW","amount":21240}`))
	})
	got, _ := c.Charge(t.Context(), request())
	if got.Succeeded || got.Code != "3ds_required" {
		t.Fatalf("3DS: %+v", got)
	}
}

func TestServerErrorIsAnError(t *testing.T) {
	c := fakeCulqi(t, func(w http.ResponseWriter, _ map[string]any) { w.WriteHeader(http.StatusBadGateway) })
	if _, err := c.Charge(t.Context(), request()); err == nil {
		t.Fatal("5xx means unknown outcome")
	}
}

func TestGetCharge(t *testing.T) {
	c := fakeCulqi(t, func(w http.ResponseWriter, body map[string]any) {
		_, _ = w.Write([]byte(`{"object":"charge","id":"` + body["get"].(string) + `","amount":100,"currency_code":"USD","response_code":"venta_exitosa","metadata":{"order_id":"x"}}`))
	})
	got, err := c.GetCharge(t.Context(), "chr_test_9")
	if err != nil || got.ID != "chr_test_9" || got.Currency != money.USD || !got.Succeeded {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestWebhookChargeID(t *testing.T) {
	cases := map[string]string{
		`{"object":"event","type":"charge.creation.succeeded","data":{"object":"charge","id":"chr_live_1"}}`:      "chr_live_1",
		`{"object":"event","type":"charge.creation.succeeded","data":"{\"object\":\"charge\",\"id\":\"chr_2\"}"}`: "chr_2",
		`{"object":"event","type":"order.status.changed","data":{"object":"order","id":"ord_1"}}`:                 "",
	}
	for body, want := range cases {
		got, err := WebhookChargeID([]byte(body))
		if err != nil || got != want {
			t.Errorf("%s: got %q %v", body, got, err)
		}
	}
	if _, err := WebhookChargeID([]byte("not json")); err == nil {
		t.Error("invalid body must fail")
	}
}
