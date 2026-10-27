// Package mysql reads shipping rates from MySQL.
package mysql

import (
	"context"
	"database/sql"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/money"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/mysql"
	"github.com/CodyLionVivo/duradent/services/api/internal/shipping/domain"
)

type Rates struct{ db *sql.DB }

func New(db *sql.DB) *Rates { return &Rates{db: db} }

func (r *Rates) All(ctx context.Context) ([]domain.Rate, error) {
	rows, err := mysql.Conn(ctx, r.db).QueryContext(ctx, "SELECT zone, max_weight_g, amount_pen FROM shipping_rates ORDER BY zone, max_weight_g")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []domain.Rate
	for rows.Next() {
		var rt domain.Rate
		var pen string
		if err := rows.Scan(&rt.Zone, &rt.MaxWeightG, &pen); err != nil {
			return nil, err
		}
		if rt.Amount, err = money.Parse(pen, money.PEN); err != nil {
			return nil, err
		}
		out = append(out, rt)
	}
	return out, rows.Err()
}
