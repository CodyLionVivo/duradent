package nubefact

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/invoicing/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/invoicing/ports"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/money"
)

func server(t *testing.T, status int, reply string, got *map[string]any) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "tok-123" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, got)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, "tok-123")
}

func doc() domain.Document {
	return domain.Document{
		Type: domain.CreditNote, Series: "FC01", Number: 3, IssueDate: time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC),
		Currency: money.USD, ExchangeRate: "3.750",
		Customer:   domain.Customer{DocType: "RUC", DocNumber: "20123456789", Name: "Clínica Sonrisa SAC", Address: "Av. Arequipa 123"},
		Lines:      []domain.DocLine{{Unit: "NIU", Code: "BRK", Description: "Bracket", Qty: 2, UnitValue: 5000, Subtotal: 10000, IGV: 1800, Total: 11800}},
		TaxedTotal: 10000, IGV: 1800, Total: 11800,
		RefType: domain.Factura, RefSeries: "F001", RefNumber: 9, Reason: "Pedido reembolsado",
	}
}

func TestIssueSendsSunatFields(t *testing.T) {
	var sent map[string]any
	c := server(t, http.StatusOK, `{"tipo_de_comprobante":3,"serie":"FC01","numero":3,"aceptada_por_sunat":true,
		"sunat_description":"La Nota de Credito numero FC01-3, ha sido aceptada","enlace_del_pdf":"https://nubefact/x.pdf","codigo_hash":"abc="}`, &sent)
	res, err := c.Issue(t.Context(), doc())
	if err != nil {
		t.Fatal(err)
	}
	if res.PDFURL != "https://nubefact/x.pdf" || res.Hash != "abc=" {
		t.Fatalf("%+v", res)
	}
	want := map[string]any{
		"operacion": "generar_comprobante", "tipo_de_comprobante": 3.0, "serie": "FC01", "numero": 3.0,
		"cliente_tipo_de_documento": "6", "fecha_de_emision": "26-10-2026", "moneda": 2.0, "tipo_de_cambio": 3.75,
		"total_gravada": 100.0, "total_igv": 18.0, "total": 118.0, "documento_que_se_modifica_tipo": 1.0,
		"documento_que_se_modifica_serie": "F001", "tipo_de_nota_de_credito": 1.0,
	}
	for k, v := range want {
		if sent[k] != v {
			t.Errorf("%s = %v, want %v", k, sent[k], v)
		}
	}
	line := sent["items"].([]any)[0].(map[string]any)
	if line["valor_unitario"] != 50.0 || line["precio_unitario"] != 59.0 || line["igv"] != 18.0 || line["tipo_de_igv"] != 1.0 {
		t.Fatalf("line %+v", line)
	}
}

func TestErrors(t *testing.T) {
	var sent map[string]any
	c := server(t, http.StatusBadRequest, `{"errors":"Este documento ya existe en NubeFacT","codigo":23}`, &sent)
	if _, err := c.Issue(t.Context(), doc()); !errors.Is(err, ports.ErrAlreadyExists) {
		t.Fatalf("code 23: %v", err)
	}
	c = server(t, http.StatusBadRequest, `{"errors":"El RUC del cliente no es válido","codigo":20}`, &sent)
	var rej *ports.Rejection
	if _, err := c.Issue(t.Context(), doc()); !errors.As(err, &rej) || rej.Code != "20" {
		t.Fatalf("rejection: %v", err)
	}
	c = server(t, http.StatusBadGateway, `<html>bad gateway</html>`, &sent)
	if _, err := c.Issue(t.Context(), doc()); err == nil || errors.As(err, &rej) {
		t.Fatalf("5xx is transient: %v", err)
	}
	c = server(t, http.StatusOK, `{"aceptada_por_sunat":false,"enlace_del_pdf":"https://nubefact/b.pdf"}`, &sent)
	res, err := c.Lookup(t.Context(), domain.Boleta, "B001", 4)
	if err != nil || res.SunatDescription == "" || sent["operacion"] != "consultar_comprobante" {
		t.Fatalf("lookup: %+v %v %v", res, err, sent)
	}
}
