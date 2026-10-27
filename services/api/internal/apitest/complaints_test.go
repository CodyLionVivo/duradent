package apitest

import (
	"net/http"
	"strings"
	"testing"

	notifdomain "github.com/CodyLionVivo/duradent/services/api/internal/notifications/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/httpserver"
)

// findMail returns the email sent to someone whose subject contains text.
func findMail(s server, to, text string) *notifdomain.Message {
	for _, m := range s.mail.all() {
		if m.To == to && strings.Contains(m.Subject, text) {
			return &m
		}
	}
	return nil
}

type complaint struct {
	ID         string  `json:"id"`
	Code       string  `json:"code"`
	Status     string  `json:"status"`
	Amount     *string `json:"amount"`
	Response   string  `json:"response"`
	AnsweredBy string  `json:"answered_by"`
	Provider   struct {
		RUC string `json:"ruc"`
	} `json:"provider"`
}

func sheetInput() map[string]any {
	return map[string]any{
		"kind": "reclamo", "name": "Ana Torres", "document_type": "DNI", "document_number": "12345678",
		"address": "Av. Arequipa 123, Lima", "phone": "999888777", "email": "ana@clinica.pe", "item_type": "producto",
		"order_number": "DD-000012", "amount": "250.00", "item_description": "Brackets metálicos Roth",
		"detail": "Llegó una caja abierta.", "request": "Cambio de la caja.", "response_channel": "email", "locale": "en",
	}
}

func TestComplaintsBook(t *testing.T) {
	s := newServer(t)

	var info struct {
		Provider struct {
			Name string `json:"name"`
			RUC  string `json:"ruc"`
		} `json:"provider"`
		ResponseDays int `json:"response_days"`
	}
	if st := s.do(http.MethodGet, "/v1/complaints/provider", false, nil, &info); st != http.StatusOK || info.Provider.RUC != "20123456789" || info.ResponseDays != 15 {
		t.Fatalf("provider: %d %+v", st, info)
	}

	// Anyone can file a sheet; a retry with the same key returns the same one.
	anon := s.with("Idempotency-Key", "form-load-1")
	var first, again complaint
	if st := anon.do(http.MethodPost, "/v1/complaints", false, sheetInput(), &first); st != http.StatusCreated {
		t.Fatalf("file: %d", st)
	}
	if !strings.HasPrefix(first.Code, "000001-") || first.Status != "open" || first.Amount == nil || *first.Amount != "250.00" || first.Provider.RUC != "20123456789" {
		t.Fatalf("sheet: %+v", first)
	}
	if st := anon.do(http.MethodPost, "/v1/complaints", false, sheetInput(), &again); st != http.StatusCreated || again.ID != first.ID {
		t.Fatalf("retry must return the same sheet: %d %+v", st, again)
	}
	bad := sheetInput()
	bad["email"] = "ana@clinica"
	var p httpserver.Problem
	if st := s.do(http.MethodPost, "/v1/complaints", false, bad, &p); st != http.StatusUnprocessableEntity || p.Code != "complaints.email_invalid" {
		t.Fatalf("invalid sheet: %d %+v", st, p)
	}
	var second complaint
	s.do(http.MethodPost, "/v1/complaints", false, sheetInput(), &second)
	if !strings.HasPrefix(second.Code, "000002-") {
		t.Fatalf("numbers are correlative: %s", second.Code)
	}

	// The consumer gets a copy in their language; operations get an alert.
	drain(t, s)
	copyMail, alert := findMail(s, "ana@clinica.pe", "Complaint sheet No. "+first.Code), findMail(s, adminEmail, "N.° "+first.Code)
	if copyMail == nil || !strings.Contains(copyMail.Text, "Request: Cambio de la caja.") || !strings.Contains(copyMail.Text, "RUC 20123456789") {
		t.Fatalf("consumer copy: %+v", copyMail)
	}
	if alert == nil || !strings.Contains(alert.HTML, "/admin/complaints/"+first.ID) {
		t.Fatalf("ops alert: %+v", alert)
	}

	// Only admins see the book.
	if st := s.do(http.MethodGet, "/v1/admin/complaints", false, nil, nil); st != http.StatusUnauthorized && st != http.StatusForbidden {
		t.Fatalf("anonymous list: %d", st)
	}
	var page struct {
		Items []complaint `json:"items"`
		Total int         `json:"total"`
		Open  int         `json:"open"`
	}
	if st := s.do(http.MethodGet, "/v1/admin/complaints?q="+first.Code, true, nil, &page); st != http.StatusOK || page.Total != 1 || page.Open != 2 || page.Items[0].ID != first.ID {
		t.Fatalf("admin list: %d %+v", st, page)
	}

	var answered complaint
	answer := map[string]any{"response": "Cambiamos la caja el lunes.\nGracias por avisarnos."}
	if st := s.do(http.MethodPost, "/v1/admin/complaints/"+first.ID+"/answer", true, answer, &answered); st != http.StatusOK || answered.Status != "answered" || answered.AnsweredBy != "admin" {
		t.Fatalf("answer: %d %+v", st, answered)
	}
	if st := s.do(http.MethodPost, "/v1/admin/complaints/"+first.ID+"/answer", true, answer, &p); st != http.StatusConflict || p.Code != "complaints.already_answered" {
		t.Fatalf("second answer: %d %+v", st, p)
	}
	drain(t, s)
	reply := findMail(s, "ana@clinica.pe", "Answer to your complaint sheet No. "+first.Code)
	if reply == nil || !strings.Contains(reply.HTML, "Cambiamos la caja el lunes.<br>Gracias por avisarnos.") {
		t.Fatalf("answer email: %+v", reply)
	}
}
