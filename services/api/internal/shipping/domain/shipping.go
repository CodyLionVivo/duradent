// Package domain prices delivery within Peru by zone and parcel weight.
package domain

import (
	"slices"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/money"
)

type Zone string

const (
	ZoneLima       Zone = "lima"       // Lima Metropolitana and Callao: own courier, next day
	ZoneProvincias Zone = "provincias" // rest of Peru: national courier
	ZonePickup     Zone = "pickup"     // collected at Duradent's store, free
)

// ZoneFor maps an INEI department code to a delivery zone.
func ZoneFor(department string) Zone {
	if department == "15" || department == "07" { // Lima, Callao
		return ZoneLima
	}
	return ZoneProvincias
}

// Rate is the price of a parcel up to MaxWeightG grams.
type Rate struct {
	Zone       Zone
	MaxWeightG int
	Amount     money.Money // soles, without IGV
}

// Quote prices a parcel. Parcels heavier than the largest tier ship as
// several parcels of that tier.
func Quote(rates []Rate, zone Zone, weightG int) (money.Money, error) {
	if zone == ZonePickup {
		return money.New(0, money.PEN), nil
	}
	var tiers []Rate
	for _, r := range rates {
		if r.Zone == zone {
			tiers = append(tiers, r)
		}
	}
	if len(tiers) == 0 {
		return money.Money{}, apperr.Invalid("shipping.zone_unavailable", "shipping", "we do not deliver to this zone yet")
	}
	slices.SortFunc(tiers, func(a, b Rate) int { return a.MaxWeightG - b.MaxWeightG })
	if weightG < 0 {
		weightG = 0
	}
	for _, t := range tiers {
		if weightG <= t.MaxWeightG {
			return t.Amount, nil
		}
	}
	largest := tiers[len(tiers)-1]
	parcels := (weightG + largest.MaxWeightG - 1) / largest.MaxWeightG
	return largest.Amount.Mul(int64(parcels)), nil
}
