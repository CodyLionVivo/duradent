package application_test

import (
	"context"
	"testing"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/money"
	"github.com/CodyLionVivo/duradent/services/api/internal/pricing/application"
	"github.com/CodyLionVivo/duradent/services/api/internal/pricing/domain"
)

type memLists struct{ all []*domain.PriceList }

func (m *memLists) Save(_ context.Context, pl *domain.PriceList) error {
	if pl.IsDefault {
		for _, o := range m.all {
			o.IsDefault = o.ID == pl.ID
		}
	}
	for _, o := range m.all {
		if o.ID == pl.ID {
			return nil
		}
	}
	m.all = append(m.all, pl)
	return nil
}
func (m *memLists) ByID(_ context.Context, lid id.ID) (*domain.PriceList, error) {
	for _, l := range m.all {
		if l.ID == lid {
			return l, nil
		}
	}
	return nil, apperr.NotFound("pricing.list_not_found", "nope")
}
func (m *memLists) ByCode(_ context.Context, code string) (*domain.PriceList, error) {
	for _, l := range m.all {
		if l.Code == code {
			return l, nil
		}
	}
	return nil, apperr.NotFound("pricing.list_not_found", "nope")
}
func (m *memLists) Default(_ context.Context) (*domain.PriceList, error) {
	for _, l := range m.all {
		if l.IsDefault {
			return l, nil
		}
	}
	return nil, apperr.NotFound("pricing.default_missing", "nope")
}
func (m *memLists) List(context.Context) ([]*domain.PriceList, error) { return m.all, nil }

type memPrices map[id.ID]map[id.ID]domain.VariantPrices

func (m memPrices) Replace(_ context.Context, vp domain.VariantPrices) error {
	if m[vp.PriceListID] == nil {
		m[vp.PriceListID] = map[id.ID]domain.VariantPrices{}
	}
	m[vp.PriceListID][vp.VariantID] = vp
	return nil
}
func (m memPrices) ForVariants(_ context.Context, lid id.ID, vids []id.ID) (map[id.ID]domain.VariantPrices, error) {
	out := map[id.ID]domain.VariantPrices{}
	for _, v := range vids {
		if vp, ok := m[lid][v]; ok {
			out[v] = vp
		}
	}
	return out, nil
}

func TestQuoteFallsBackToDefaultList(t *testing.T) {
	ctx := context.Background()
	svc := application.NewService(&memLists{}, memPrices{})
	public, _ := svc.CreatePriceList(ctx, "public", "Público", true)
	clinic, _ := svc.CreatePriceList(ctx, "clinica", "Clínicas", false)

	bracket, wire, unpriced := id.New(), id.New(), id.New()
	pen := func(m int64) money.Money { return money.New(m, money.PEN) }
	mustSet := func(l *domain.PriceList, v id.ID, tiers ...domain.Tier) {
		if _, err := svc.SetVariantPrices(ctx, l.ID, v, tiers); err != nil {
			t.Fatal(err)
		}
	}
	mustSet(public, bracket, domain.Tier{MinQty: 1, Unit: pen(1500)})
	mustSet(public, wire, domain.Tier{MinQty: 1, Unit: pen(800)})
	mustSet(clinic, bracket, domain.Tier{MinQty: 1, Unit: pen(1200)}, domain.Tier{MinQty: 20, Unit: pen(1000)})

	quotes, err := svc.Quote(ctx, application.QuoteQuery{ListCode: "clinica", Currency: money.PEN, VariantIDs: []id.ID{bracket, wire, unpriced}, Qty: 20})
	if err != nil {
		t.Fatal(err)
	}
	if q := quotes[bracket]; q.Unit.Minor != 1000 || q.ListCode != "clinica" || len(q.Tiers) != 2 {
		t.Errorf("bracket from clinic list with volume tier: %+v", q)
	}
	if q := quotes[wire]; q.Unit.Minor != 800 || q.ListCode != "public" {
		t.Errorf("wire should fall back to the public list: %+v", q)
	}
	if _, ok := quotes[unpriced]; ok {
		t.Error("unpriced variant must be absent")
	}

	anon, _ := svc.Quote(ctx, application.QuoteQuery{Currency: money.PEN, VariantIDs: []id.ID{bracket}})
	if anon[bracket].Unit.Minor != 1500 {
		t.Errorf("anonymous visitors see the default list: %+v", anon[bracket])
	}
	usd, _ := svc.Quote(ctx, application.QuoteQuery{Currency: money.USD, VariantIDs: []id.ID{bracket}})
	if len(usd) != 0 {
		t.Errorf("no USD prices yet: %+v", usd)
	}
}

func TestOnlyOneDefaultList(t *testing.T) {
	ctx := context.Background()
	svc := application.NewService(&memLists{}, memPrices{})
	public, _ := svc.CreatePriceList(ctx, "public", "Público", true)
	clinic, _ := svc.CreatePriceList(ctx, "clinica", "Clínicas", false)

	if _, err := svc.UpdatePriceList(ctx, public.ID, "public", "Público", false); !apperr.IsKind(err, apperr.KindConflict) {
		t.Fatalf("cannot unset the default directly: %v", err)
	}
	if _, err := svc.UpdatePriceList(ctx, clinic.ID, "clinica", "Clínicas", true); err != nil {
		t.Fatal(err)
	}
	if public.IsDefault || !clinic.IsDefault {
		t.Fatal("making clinic default must clear public")
	}
	if _, err := svc.CreatePriceList(ctx, "clinica", "Otra", false); !apperr.IsKind(err, apperr.KindConflict) {
		t.Fatalf("duplicate code: %v", err)
	}
}
