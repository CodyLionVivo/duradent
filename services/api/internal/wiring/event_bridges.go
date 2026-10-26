package wiring

import (
	"context"
	"log/slog"
	"strings"

	invoicingdomain "github.com/CodyLionVivo/duradent/services/api/internal/invoicing/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/locale"
	notifapp "github.com/CodyLionVivo/duradent/services/api/internal/notifications/application"
	notifports "github.com/CodyLionVivo/duradent/services/api/internal/notifications/ports"
	ordersdomain "github.com/CodyLionVivo/duradent/services/api/internal/orders/domain"
)

// Bridges for the event-driven contexts (notifications, invoicing) and for
// identity's verification emails.

// --- notifications -----------------------------------------------------------------

type notifOrders struct{ app *App }

func (b notifOrders) Order(ctx context.Context, orderID id.ID) (notifports.OrderInfo, error) {
	d, err := b.app.Orders.Detail(ctx, orderID)
	if err != nil {
		return notifports.OrderInfo{}, err
	}
	o := d.Order
	return notifports.OrderInfo{
		Number: o.Number, AccountID: o.AccountID, Email: o.Email, Total: o.Total, Method: string(o.Method),
		PaymentDeadline: o.PaymentDeadline, Carrier: o.Carrier, Tracking: o.Tracking, CancelReason: o.CancelReason,
	}, nil
}

type notifAccounts struct{ app *App }

func (b notifAccounts) Recipient(ctx context.Context, accountID id.ID) (notifports.Recipient, error) {
	p, err := b.app.Identity.Profile(ctx, accountID)
	if err != nil {
		return notifports.Recipient{}, err
	}
	return notifports.Recipient{Email: p.Account.Email, Name: p.Account.DisplayName, Locale: p.Account.Locale}, nil
}

// verificationMailer lets identity send its confirmation link through the
// notifications context's templates and transport.
type verificationMailer struct {
	svc *notifapp.Service
	log *slog.Logger
}

func (b verificationMailer) SendVerification(ctx context.Context, email, name string, l locale.Locale, token string) error {
	err := b.svc.SendVerification(ctx, notifports.Recipient{Email: email, Name: name, Locale: l}, token)
	if err != nil {
		b.log.ErrorContext(ctx, "send verification email", "error", err)
	}
	return err
}

// --- invoicing ---------------------------------------------------------------------

type invoicingOrders struct{ app *App }

func (b invoicingOrders) Sale(ctx context.Context, orderID id.ID) (invoicingdomain.Sale, error) {
	d, err := b.app.Orders.Detail(ctx, orderID)
	if err != nil {
		return invoicingdomain.Sale{}, err
	}
	o := d.Order
	s := invoicingdomain.Sale{
		OrderID: o.ID, Number: o.Number, AccountID: o.AccountID,
		Customer: invoicingdomain.Customer{
			InvoiceType: o.Billing.InvoiceType, DocType: o.Billing.DocumentType, DocNumber: o.Billing.DocumentNumber,
			Name: o.Billing.BusinessName, Address: b.address(ctx, o), Email: o.Email,
		},
		Shipping: o.ShippingAmount, Tax: o.Tax, Total: o.Total,
	}
	if o.PaidAt != nil {
		s.PaidAt = *o.PaidAt
	}
	for _, l := range o.Lines {
		name := l.Name
		if l.Options != "" {
			name += " (" + l.Options + ")"
		}
		s.Lines = append(s.Lines, invoicingdomain.SaleLine{SKU: l.SKU, Name: name, UnitPrice: l.UnitPrice, Qty: l.Qty, LineTotal: l.LineTotal})
	}
	return s, nil
}

// address is the delivery address of the order or, for pickups, the
// customer's default address.
func (b invoicingOrders) address(ctx context.Context, o *ordersdomain.Order) string {
	join := func(parts ...string) string {
		var out []string
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return strings.Join(out, ", ")
	}
	if sh := o.Shipping; !sh.Pickup {
		return join(sh.Line1, sh.District, sh.Province, sh.Department)
	}
	p, err := b.app.Identity.Profile(ctx, o.AccountID)
	if err != nil {
		return ""
	}
	for _, a := range p.Addresses {
		if a.IsDefault {
			return join(a.Line1, a.District, a.Province, a.Department)
		}
	}
	return ""
}

func (b invoicingOrders) Owner(ctx context.Context, number string) (id.ID, id.ID, error) {
	o, err := b.app.Orders.ByNumber(ctx, number)
	if err != nil {
		return id.Nil, id.Nil, err
	}
	return o.ID, o.AccountID, nil
}
