package nubefact

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/CodyLionVivo/duradent/services/api/internal/invoicing/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/invoicing/ports"
)

// Fake accepts every document without calling Nubefact (local development
// without an account, and tests). Customers named "*rechazo*" are rejected.
// Never used outside APP_ENV=local.
type Fake struct {
	mu     sync.Mutex
	issued map[string]domain.Document
}

func NewFake() *Fake { return &Fake{issued: map[string]domain.Document{}} }

var _ ports.Provider = (*Fake)(nil)

func (f *Fake) Name() string { return "fake" }

func key(series string, number int) string { return fmt.Sprintf("%s-%d", series, number) }

func (f *Fake) Issue(_ context.Context, d domain.Document) (domain.Result, error) {
	if strings.Contains(strings.ToLower(d.Customer.Name), "rechazo") {
		return domain.Result{}, &ports.Rejection{Code: "20", Message: "El número de RUC del cliente no es válido (simulación)"}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	k := key(d.Series, d.Number)
	if _, ok := f.issued[k]; ok {
		return domain.Result{}, ports.ErrAlreadyExists
	}
	f.issued[k] = d
	return result(k), nil
}

func (f *Fake) Lookup(_ context.Context, _ domain.DocType, series string, number int) (domain.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := key(series, number)
	if _, ok := f.issued[k]; !ok {
		return domain.Result{}, fmt.Errorf("fake OSE: %s not found", k)
	}
	return result(k), nil
}

// Issued returns what was sent (tests).
func (f *Fake) Issued() map[string]domain.Document {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]domain.Document, len(f.issued))
	for k, v := range f.issued {
		out[k] = v
	}
	return out
}

func result(k string) domain.Result {
	return domain.Result{
		SunatDescription: "Simulación local: no se envió a la SUNAT",
		Hash:             "fake-" + k,
		PDFURL:           "https://example.invalid/comprobantes/" + k + ".pdf",
	}
}
