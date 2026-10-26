package mysql_test

import (
	"context"
	"sync"
	"testing"
	"time"

	inventorymysql "github.com/CodyLionVivo/duradent/services/api/internal/inventory/adapters/mysql"
	"github.com/CodyLionVivo/duradent/services/api/internal/inventory/application"
	"github.com/CodyLionVivo/duradent/services/api/internal/inventory/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/mysql"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/mysqltest"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/outbox"
	"github.com/CodyLionVivo/duradent/services/api/migrations"
)

func setup(t *testing.T) *application.Service {
	t.Helper()
	db := mysqltest.Start(t)
	if err := mysql.Migrate(db, migrations.FS, "."); err != nil {
		t.Fatal(err)
	}
	now := func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }
	return application.NewService(inventorymysql.New(db), mysql.NewUnitOfWork(db), outbox.NewWriter(db), now)
}

func TestReserveConfirmReleaseAgainstMySQL(t *testing.T) {
	svc := setup(t)
	ctx := context.Background()
	a, b := id.New(), id.New()
	if _, err := svc.Adjust(ctx, a, 10, domain.MovementPurchase, "compra inicial"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Adjust(ctx, b, 3, domain.MovementPurchase, ""); err != nil {
		t.Fatal(err)
	}

	order1, order2 := id.New(), id.New()
	if err := svc.Reserve(ctx, order1, []domain.Line{{VariantID: a, Qty: 4}, {VariantID: b, Qty: 1}, {VariantID: a, Qty: 2}}); err != nil {
		t.Fatal(err)
	}
	avail, _ := svc.Availability(ctx, []id.ID{a, b})
	if avail[a] != 4 || avail[b] != 2 {
		t.Fatalf("after reserve: %v", avail)
	}

	// Not enough of b: nothing is reserved, and the error names the variant.
	err := svc.Reserve(ctx, order2, []domain.Line{{VariantID: a, Qty: 1}, {VariantID: b, Qty: 5}})
	e, ok := apperr.As(err)
	if !ok || e.Code != "inventory.insufficient_stock" || e.Field != b.String()+":2" {
		t.Fatalf("short reservation: %v", err)
	}
	if avail, _ = svc.Availability(ctx, []id.ID{a}); avail[a] != 4 {
		t.Fatalf("a failed reservation must not hold anything: %v", avail)
	}

	if err := svc.Confirm(ctx, order1); err != nil {
		t.Fatal(err)
	}
	stock, _ := svc.Stock(ctx, []id.ID{a, b})
	if stock[a].OnHand != 4 || stock[a].Reserved != 0 || stock[b].OnHand != 2 {
		t.Fatalf("after confirm: %+v %+v", stock[a], stock[b])
	}
	if err := svc.Confirm(ctx, order1); err != nil {
		t.Fatal("confirming twice is a no-op")
	}
	if stock2, _ := svc.Stock(ctx, []id.ID{a}); stock2[a].OnHand != 4 {
		t.Fatal("double confirm must not take stock twice")
	}

	if err := svc.Reserve(ctx, order2, []domain.Line{{VariantID: a, Qty: 4}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Adjust(ctx, a, -1, domain.MovementAdjustment, "conteo"); !apperr.IsKind(err, apperr.KindConflict) {
		t.Fatalf("cannot drop below reserved: %v", err)
	}
	if err := svc.Release(ctx, order2); err != nil {
		t.Fatal(err)
	}
	if avail, _ = svc.Availability(ctx, []id.ID{a}); avail[a] != 4 {
		t.Fatalf("released units return: %v", avail)
	}
	moves, _ := svc.Movements(ctx, a)
	if len(moves) != 2 || moves[0].Reason != domain.MovementSale || moves[0].Delta != -6 {
		t.Fatalf("movements: %+v", moves)
	}
}

func TestConcurrentReservationsNeverOversell(t *testing.T) {
	svc := setup(t)
	ctx := context.Background()
	v := id.New()
	if _, err := svc.Adjust(ctx, v, 5, domain.MovementPurchase, ""); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := svc.Reserve(ctx, id.New(), []domain.Line{{VariantID: v, Qty: 1}}); err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	stock, _ := svc.Stock(ctx, []id.ID{v})
	if ok != 5 || stock[v].Reserved != 5 {
		t.Fatalf("12 buyers for 5 units: %d succeeded, reserved %d", ok, stock[v].Reserved)
	}
}
