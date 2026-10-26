// Package mysql stores price lists and tiered prices in MySQL.
package mysql

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/money"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/mysql"
	"github.com/CodyLionVivo/duradent/services/api/internal/pricing/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/pricing/ports"
)

type PriceLists struct{ db *sql.DB }

func NewPriceLists(db *sql.DB) *PriceLists { return &PriceLists{db: db} }

var _ ports.PriceListRepository = (*PriceLists)(nil)

func (r *PriceLists) Save(ctx context.Context, pl *domain.PriceList) error {
	err := mysql.InTx(ctx, r.db, func(tx *sql.Tx) error {
		now := time.Now().UTC()
		if pl.IsDefault {
			if _, err := tx.ExecContext(ctx, "UPDATE price_lists SET is_default = FALSE WHERE id <> ?", pl.ID); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO price_lists (id, code, name, is_default, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE code = IF(id = VALUES(id), VALUES(code), code),
				name = IF(id = VALUES(id), VALUES(name), name),
				is_default = IF(id = VALUES(id), VALUES(is_default), is_default),
				updated_at = IF(id = VALUES(id), VALUES(updated_at), updated_at)`,
			pl.ID, pl.Code, pl.Name, pl.IsDefault, now, now)
		return err
	})
	if _, dup := mysql.DuplicateKey(err); dup {
		return apperr.Conflict("pricing.code_taken", "code", "a list with this code already exists")
	}
	return err
}

func (r *PriceLists) ByID(ctx context.Context, listID id.ID) (*domain.PriceList, error) {
	return r.one(ctx, "id = ?", listID)
}

func (r *PriceLists) ByCode(ctx context.Context, code string) (*domain.PriceList, error) {
	return r.one(ctx, "code = ?", code)
}

func (r *PriceLists) Default(ctx context.Context) (*domain.PriceList, error) {
	pl, err := r.one(ctx, "is_default = ?", true)
	if apperr.IsKind(err, apperr.KindNotFound) {
		return nil, apperr.NotFound("pricing.default_missing", "no default price list configured")
	}
	return pl, err
}

func (r *PriceLists) one(ctx context.Context, where string, arg any) (*domain.PriceList, error) {
	pl := &domain.PriceList{}
	err := r.db.QueryRowContext(ctx, "SELECT id, code, name, is_default FROM price_lists WHERE "+where+" LIMIT 1", arg). //nolint:gosec // where is a constant
																Scan(&pl.ID, &pl.Code, &pl.Name, &pl.IsDefault)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperr.NotFound("pricing.list_not_found", "price list not found")
	}
	return pl, err
}

func (r *PriceLists) List(ctx context.Context) ([]*domain.PriceList, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT id, code, name, is_default FROM price_lists ORDER BY is_default DESC, name")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*domain.PriceList
	for rows.Next() {
		pl := &domain.PriceList{}
		if err := rows.Scan(&pl.ID, &pl.Code, &pl.Name, &pl.IsDefault); err != nil {
			return nil, err
		}
		out = append(out, pl)
	}
	return out, rows.Err()
}

type Prices struct{ db *sql.DB }

func NewPrices(db *sql.DB) *Prices { return &Prices{db: db} }

var _ ports.PriceRepository = (*Prices)(nil)

func (r *Prices) Replace(ctx context.Context, vp domain.VariantPrices) error {
	return mysql.InTx(ctx, r.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM prices WHERE price_list_id = ? AND variant_id = ?", vp.PriceListID, vp.VariantID); err != nil {
			return err
		}
		for _, t := range vp.Tiers {
			if _, err := tx.ExecContext(ctx,
				"INSERT INTO prices (price_list_id, variant_id, currency, min_qty, amount) VALUES (?, ?, ?, ?, ?)",
				vp.PriceListID, vp.VariantID, string(t.Unit.Currency), t.MinQty, t.Unit.Decimal()); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *Prices) ForVariants(ctx context.Context, listID id.ID, variantIDs []id.ID) (map[id.ID]domain.VariantPrices, error) {
	out := map[id.ID]domain.VariantPrices{}
	if len(variantIDs) == 0 {
		return out, nil
	}
	args := []any{listID}
	for _, v := range variantIDs {
		args = append(args, v)
	}
	rows, err := r.db.QueryContext(ctx, //nolint:gosec // only placeholders are interpolated
		"SELECT variant_id, currency, min_qty, amount FROM prices WHERE price_list_id = ? AND variant_id IN ("+
			mysql.Placeholders(len(variantIDs))+") ORDER BY variant_id, currency, min_qty", args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var vid id.ID
		var currency, amount string
		var minQty int
		if err := rows.Scan(&vid, &currency, &minQty, &amount); err != nil {
			return nil, err
		}
		unit, err := money.Parse(amount, money.Currency(currency))
		if err != nil {
			return nil, err
		}
		vp := out[vid]
		vp.PriceListID, vp.VariantID = listID, vid
		vp.Tiers = append(vp.Tiers, domain.Tier{MinQty: minQty, Unit: unit})
		out[vid] = vp
	}
	return out, rows.Err()
}
