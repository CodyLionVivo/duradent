// Package application quotes delivery for checkout.
package application

import (
	"context"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/money"
	"github.com/CodyLionVivo/duradent/services/api/internal/shipping/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/shipping/ports"
)

type Service struct{ rates ports.RateRepository }

func NewService(r ports.RateRepository) *Service { return &Service{rates: r} }

// Quote prices delivery to a department (INEI code), or pickup when
// department is empty and pickup is true.
func (s *Service) Quote(ctx context.Context, department string, pickup bool, weightG int, c money.Currency) (domain.Zone, money.Money, error) {
	zone := domain.ZonePickup
	if !pickup {
		zone = domain.ZoneFor(department)
	}
	rates, err := s.rates.All(ctx)
	if err != nil {
		return "", money.Money{}, err
	}
	amount, err := domain.Quote(rates, zone, weightG, c)
	return zone, amount, err
}

func (s *Service) Rates(ctx context.Context) ([]domain.Rate, error) {
	return s.rates.All(ctx)
}
