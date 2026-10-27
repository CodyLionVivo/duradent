// Package nubefact issues electronic documents through Nubefact's JSON API
// (OSE/PSE). Each account has its own route URL and token; both come from
// Secrets Manager. Reference: "Manual de integración API JSON" (Nubefact).
package nubefact

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/invoicing/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/invoicing/ports"
)

type Client struct {
	url   string
	token string
	http  *http.Client
}

func New(url, token string) *Client {
	return &Client{url: url, token: token, http: &http.Client{Timeout: 60 * time.Second}}
}

var _ ports.Provider = (*Client)(nil)

func (c *Client) Name() string { return "nubefact" }

// amount writes minor units as a JSON number with two decimals.
func amount(minor int64) json.Number {
	sign := ""
	if minor < 0 {
		sign, minor = "-", -minor
	}
	return json.Number(fmt.Sprintf("%s%d.%02d", sign, minor/100, minor%100))
}

func docTypeCode(t domain.DocType) int {
	switch t {
	case domain.Factura:
		return 1
	case domain.Boleta:
		return 2
	case domain.CreditNote:
	}
	return 3
}

func customerDocCode(t string) string {
	switch strings.ToUpper(t) {
	case "RUC":
		return "6"
	case "CE":
		return "4"
	default:
		return "1" // DNI
	}
}

type item struct {
	Unit           string      `json:"unidad_de_medida"`
	Code           string      `json:"codigo"`
	Description    string      `json:"descripcion"`
	Qty            int         `json:"cantidad"`
	UnitValue      json.Number `json:"valor_unitario"`
	UnitPrice      json.Number `json:"precio_unitario"`
	Subtotal       json.Number `json:"subtotal"`
	IGVType        int         `json:"tipo_de_igv"` // 1: gravado, operación onerosa
	IGV            json.Number `json:"igv"`
	Total          json.Number `json:"total"`
	AdvancePayment bool        `json:"anticipo_regularizacion"`
}

type request struct {
	Operation         string      `json:"operacion"`
	Type              int         `json:"tipo_de_comprobante"`
	Series            string      `json:"serie"`
	Number            int         `json:"numero"`
	SunatTransaction  int         `json:"sunat_transaction,omitempty"`
	CustomerDocType   string      `json:"cliente_tipo_de_documento,omitempty"`
	CustomerDocNumber string      `json:"cliente_numero_de_documento,omitempty"`
	CustomerName      string      `json:"cliente_denominacion,omitempty"`
	CustomerAddress   string      `json:"cliente_direccion,omitempty"`
	CustomerEmail     string      `json:"cliente_email,omitempty"`
	IssueDate         string      `json:"fecha_de_emision,omitempty"`
	Currency          int         `json:"moneda,omitempty"` // 1: soles
	IGVPercent        json.Number `json:"porcentaje_de_igv,omitempty"`
	TaxedTotal        json.Number `json:"total_gravada,omitempty"`
	TotalIGV          json.Number `json:"total_igv,omitempty"`
	Total             json.Number `json:"total,omitempty"`
	SendToSunat       bool        `json:"enviar_automaticamente_a_la_sunat"`
	SendToCustomer    bool        `json:"enviar_automaticamente_al_cliente"`
	Items             []item      `json:"items,omitempty"`
	// Credit notes
	RefType        int    `json:"documento_que_se_modifica_tipo,omitempty"`
	RefSeries      string `json:"documento_que_se_modifica_serie,omitempty"`
	RefNumber      int    `json:"documento_que_se_modifica_numero,omitempty"`
	CreditNoteType int    `json:"tipo_de_nota_de_credito,omitempty"`
	Observations   string `json:"observaciones,omitempty"`
}

type response struct {
	AcceptedBySunat  bool   `json:"aceptada_por_sunat"`
	SunatDescription string `json:"sunat_description"`
	Hash             string `json:"codigo_hash"`
	PDF              string `json:"enlace_del_pdf"`
	XML              string `json:"enlace_del_xml"`
	CDR              string `json:"enlace_del_cdr"`
	Errors           any    `json:"errors"`
	Code             any    `json:"codigo"`
}

func toRequest(d domain.Document) request {
	r := request{
		Operation: "generar_comprobante", Type: docTypeCode(d.Type), Series: d.Series, Number: d.Number,
		SunatTransaction: 1, CustomerDocType: customerDocCode(d.Customer.DocType), CustomerDocNumber: d.Customer.DocNumber,
		CustomerName: d.Customer.Name, CustomerAddress: d.Customer.Address, CustomerEmail: d.Customer.Email,
		IssueDate: d.IssueDate.Format("02-01-2006"), Currency: 1, IGVPercent: "18.00",
		TaxedTotal: amount(d.TaxedTotal), TotalIGV: amount(d.IGV), Total: amount(d.Total),
		SendToSunat: true, SendToCustomer: false,
	}
	if d.Type == domain.CreditNote {
		r.RefType, r.RefSeries, r.RefNumber = docTypeCode(d.RefType), d.RefSeries, d.RefNumber
		r.CreditNoteType = 1 // anulación de la operación
		r.Observations = d.Reason
	}
	for _, l := range d.Lines {
		r.Items = append(r.Items, item{
			Unit: l.Unit, Code: l.Code, Description: l.Description, Qty: l.Qty,
			UnitValue: amount(l.UnitValue), UnitPrice: json.Number(l.UnitPrice()), Subtotal: amount(l.Subtotal),
			IGVType: 1, IGV: amount(l.IGV), Total: amount(l.Total),
		})
	}
	return r
}

func (c *Client) Issue(ctx context.Context, d domain.Document) (domain.Result, error) {
	return c.call(ctx, toRequest(d))
}

func (c *Client) Lookup(ctx context.Context, t domain.DocType, series string, number int) (domain.Result, error) {
	return c.call(ctx, request{Operation: "consultar_comprobante", Type: docTypeCode(t), Series: series, Number: number})
}

func (c *Client) call(ctx context.Context, body request) (domain.Result, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return domain.Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(raw))
	if err != nil {
		return domain.Result{}, err
	}
	req.Header.Set("Authorization", c.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return domain.Result{}, fmt.Errorf("nubefact: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	out, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return domain.Result{}, err
	}
	var r response
	if err := json.Unmarshal(out, &r); err != nil {
		if res.StatusCode >= 500 {
			return domain.Result{}, fmt.Errorf("nubefact: status %d", res.StatusCode)
		}
		return domain.Result{}, fmt.Errorf("nubefact: unreadable response (status %d)", res.StatusCode)
	}
	if r.Errors != nil && r.Errors != "" {
		code := fmt.Sprint(r.Code)
		if code == "23" {
			return domain.Result{}, ports.ErrAlreadyExists
		}
		msg := fmt.Sprint(r.Errors)
		if res.StatusCode >= 500 {
			return domain.Result{}, fmt.Errorf("nubefact: %s", msg)
		}
		// Data or account errors: sending the same document again will not help.
		return domain.Result{}, &ports.Rejection{Code: code, Message: msg}
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return domain.Result{}, fmt.Errorf("nubefact: status %d", res.StatusCode)
	}
	desc := r.SunatDescription
	if desc == "" && !r.AcceptedBySunat {
		desc = "Registrado en el OSE; pendiente de envío a la SUNAT"
	}
	return domain.Result{SunatDescription: desc, Hash: r.Hash, PDFURL: r.PDF, XMLURL: r.XML, CDRURL: r.CDR}, nil
}
