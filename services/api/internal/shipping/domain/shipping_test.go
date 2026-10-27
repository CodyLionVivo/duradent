package domain

import (
	"testing"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/money"
)

func pen(m int64) money.Money { return money.New(m, money.PEN) }

var rates = []Rate{
	{Zone: ZoneLima, MaxWeightG: 5000, Amount: pen(1500)},
	{Zone: ZoneLima, MaxWeightG: 1000, Amount: pen(1000)},
	{Zone: ZoneProvincias, MaxWeightG: 1000, Amount: pen(1800)},
}

func TestZoneFor(t *testing.T) {
	if ZoneFor("15") != ZoneLima || ZoneFor("07") != ZoneLima || ZoneFor("04") != ZoneProvincias {
		t.Fatal("Lima and Callao are the Lima zone; the rest is provincias")
	}
}

func TestQuote(t *testing.T) {
	cases := []struct {
		zone   Zone
		weight int
		want   int64
	}{
		{ZoneLima, 300, 1000},
		{ZoneLima, 1000, 1000},
		{ZoneLima, 1001, 1500},
		{ZoneLima, 12000, 4500}, // three 5 kg parcels
		{ZoneProvincias, 200, 1800},
		{ZonePickup, 9000, 0},
	}
	for _, c := range cases {
		got, err := Quote(rates, c.zone, c.weight)
		if err != nil || got.Minor != c.want || got.Currency != money.PEN {
			t.Errorf("%s %dg: got %v %v, want %d", c.zone, c.weight, got, err, c.want)
		}
	}
	if _, err := Quote(nil, ZoneLima, 1); !apperr.IsKind(err, apperr.KindInvalid) {
		t.Fatal("no rates for a zone must be an error")
	}
}
