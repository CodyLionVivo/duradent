package mysql_test

import (
	"context"
	"testing"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/money"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/mysql"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/mysqltest"
	pricingmysql "github.com/CodyLionVivo/duradent/services/api/internal/pricing/adapters/mysql"
	"github.com/CodyLionVivo/duradent/services/api/internal/pricing/application"
	"github.com/CodyLionVivo/duradent/services/api/internal/pricing/domain"
	"github.com/CodyLionVivo/duradent/services/api/migrations"
)

func TestPricingAgainstMySQL(t *testing.T) {
	db := mysqltest.Start(t)
	if err := mysql.Migrate(db, migrations.FS, "."); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	svc := application.NewService(pricingmysql.NewPriceLists(db), pricingmysql.NewPrices(db))

	if _, err := svc.Quote(ctx, application.QuoteQuery{Currency: money.PEN}); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatalf("no default list yet: %v", err)
	}
	public, err := svc.CreatePriceList(ctx, "public", "Público", true)
	if err != nil {
		t.Fatal(err)
	}
	clinic, _ := svc.CreatePriceList(ctx, "clinica", "Clínicas", false)
	if _, err := svc.CreatePriceList(ctx, "public", "Otra", false); !apperr.IsKind(err, apperr.KindConflict) {
		t.Fatalf("duplicate code: %v", err)
	}

	v := id.New()
	pen, _ := money.Parse("15.90", money.PEN)
	usd, _ := money.Parse("4.30", money.USD)
	if _, err := svc.SetVariantPrices(ctx, public.ID, v, []domain.Tier{{MinQty: 1, Unit: pen}, {MinQty: 1, Unit: usd}}); err != nil {
		t.Fatal(err)
	}
	tier1, _ := money.Parse("12.50", money.PEN)
	tier10, _ := money.Parse("11.00", money.PEN)
	if _, err := svc.SetVariantPrices(ctx, clinic.ID, v, []domain.Tier{{MinQty: 10, Unit: tier10}, {MinQty: 1, Unit: tier1}}); err != nil {
		t.Fatal(err)
	}

	q, err := svc.Quote(ctx, application.QuoteQuery{ListCode: "clinica", Currency: money.PEN, VariantIDs: []id.ID{v}, Qty: 12})
	if err != nil || q[v].Unit.Decimal() != "11.00" || len(q[v].Tiers) != 2 {
		t.Fatalf("clinic quote: %+v %v", q[v], err)
	}
	q, _ = svc.Quote(ctx, application.QuoteQuery{ListCode: "clinica", Currency: money.USD, VariantIDs: []id.ID{v}})
	if q[v].Unit.Decimal() != "4.30" || q[v].ListCode != "public" {
		t.Fatalf("USD falls back to the default list: %+v", q[v])
	}

	// Replacing tiers removes the old ones.
	if _, err := svc.SetVariantPrices(ctx, clinic.ID, v, []domain.Tier{{MinQty: 1, Unit: tier1}}); err != nil {
		t.Fatal(err)
	}
	raw, _ := svc.PricesInList(ctx, clinic.ID, []id.ID{v})
	if len(raw[v].Tiers) != 1 {
		t.Fatalf("tiers after replace: %+v", raw[v])
	}

	// Switching the default list clears the previous default.
	if _, err := svc.UpdatePriceList(ctx, clinic.ID, "clinica", "Clínicas", true); err != nil {
		t.Fatal(err)
	}
	lists, _ := svc.PriceLists(ctx)
	defaults := 0
	for _, l := range lists {
		if l.IsDefault {
			defaults++
		}
	}
	if defaults != 1 || !lists[0].IsDefault || lists[0].Code != "clinica" {
		t.Fatalf("lists: %+v", lists)
	}
}
