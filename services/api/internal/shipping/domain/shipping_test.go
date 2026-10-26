package domain

import (
	"testing"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/money"
)

func pen(m int64) money.Money { return money.New(m, money.PEN) }
func usd(m int64) money.Money { return money.New(m, money.USD) }

var rates = []Rate{
	{Zone: ZoneLima, MaxWeightG: 5000, PEN: pen(1500), USD: usd(400)},
	{Zone: ZoneLima, MaxWeightG: 1000, PEN: pen(1000), USD: usd(270)},
	{Zone: ZoneProvincias, MaxWeightG: 1000, PEN: pen(1800), USD: usd(480)},
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
		cur    money.Currency
		want   int64
	}{
		{ZoneLima, 300, money.PEN, 1000},
		{ZoneLima, 1000, money.PEN, 1000},
		{ZoneLima, 1001, money.PEN, 1500},
		{ZoneLima, 12000, money.PEN, 4500}, // three 5 kg parcels
		{ZoneLima, 300, money.USD, 270},
		{ZoneProvincias, 200, money.PEN, 1800},
		{ZonePickup, 9000, money.PEN, 0},
	}
	for _, c := range cases {
		got, err := Quote(rates, c.zone, c.weight, c.cur)
		if err != nil || got.Minor != c.want || got.Currency != c.cur {
			t.Errorf("%s %dg %s: got %v %v, want %d", c.zone, c.weight, c.cur, got, err, c.want)
		}
	}
	if _, err := Quote(nil, ZoneLima, 1, money.PEN); !apperr.IsKind(err, apperr.KindInvalid) {
		t.Fatal("no rates for a zone must be an error")
	}
}
