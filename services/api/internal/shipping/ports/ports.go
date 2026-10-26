// Package ports declares the shipping context's persistence.
package ports

import (
	"context"

	"github.com/CodyLionVivo/duradent/services/api/internal/shipping/domain"
)

type RateRepository interface {
	All(ctx context.Context) ([]domain.Rate, error)
}
