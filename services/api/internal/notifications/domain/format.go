package domain

import (
	"fmt"
	"strings"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/locale"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/money"
)

// lima is Peru's time zone (no daylight saving), used for every date in emails.
var lima = time.FixedZone("PET", -5*60*60)

// FormatMoney writes an amount in soles the way the store shows it: "S/ 1,234.56".
func FormatMoney(m money.Money) string {
	const symbol = "S/"
	d := m.Decimal()
	sign := ""
	if strings.HasPrefix(d, "-") {
		sign, d = "-", d[1:]
	}
	whole, cents, _ := strings.Cut(d, ".")
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return fmt.Sprintf("%s%s %s.%s", sign, symbol, b.String(), cents)
}

// FormatDeadline writes a date and time in Lima, in the reader's language.
func FormatDeadline(t time.Time, l locale.Locale) string {
	t = t.In(lima)
	switch l {
	case locale.EN:
		return t.Format("Jan 2, 2006 3:04 PM") + " (Lima)"
	default:
		return t.Format("02/01/2006 15:04") + " (Lima)"
	}
}
