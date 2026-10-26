package wiring

import (
	"context"
	"sort"
	"strings"

	catalogapp "github.com/CodyLionVivo/duradent/services/api/internal/catalog/application"
	catalogdomain "github.com/CodyLionVivo/duradent/services/api/internal/catalog/domain"
	identityapp "github.com/CodyLionVivo/duradent/services/api/internal/identity/application"
	inventoryapp "github.com/CodyLionVivo/duradent/services/api/internal/inventory/application"
	inventorydomain "github.com/CodyLionVivo/duradent/services/api/internal/inventory/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/locale"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/money"
	ordersdomain "github.com/CodyLionVivo/duradent/services/api/internal/orders/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/orders/ports"
	pricingapp "github.com/CodyLionVivo/duradent/services/api/internal/pricing/application"
	shippingapp "github.com/CodyLionVivo/duradent/services/api/internal/shipping/application"
)

// The bridges implement the orders context's ports on top of the other
// contexts' application services. They are the only place where contexts
// meet, and they translate types so no context imports another's domain.

type catalogBridge struct{ svc *catalogapp.Service }

func (b catalogBridge) Variants(ctx context.Context, ids []id.ID, l locale.Locale) (map[id.ID]ports.VariantInfo, error) {
	refs, err := b.svc.Variants(ctx, ids)
	if err != nil {
		return nil, err
	}
	attrs, err := b.svc.Attributes(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[id.ID]ports.VariantInfo, len(refs))
	for vid, r := range refs {
		info := ports.VariantInfo{
			VariantID: vid, SKU: r.Variant.SKU, ProductName: r.Product.Name(l), ProductSlug: r.Product.Slug,
			Options: optionLabels(attrs, r.Variant.Options, l), WeightG: r.Variant.WeightG,
			Purchasable: r.Product.Status == catalogdomain.StatusActive && !r.Variant.Archived,
		}
		if len(r.Product.Media) > 0 {
			info.ImageURL = b.svc.MediaURL(r.Product.Media[0].Key)
		}
		out[vid] = info
	}
	return out, nil
}

// optionLabels renders {"slot":"022","arch":"upper"} as "Slot .022" / "Arcada Superior",
// in attribute order.
func optionLabels(attrs []*catalogdomain.Attribute, opts map[string]string, l locale.Locale) string {
	var parts []string
	for _, a := range attrs {
		code, ok := opts[a.Code]
		if !ok {
			continue
		}
		label := code
		if v, ok := a.Value(code); ok {
			label = v.Labels.In(l)
		}
		parts = append(parts, a.Names.In(l)+" "+label)
	}
	return strings.Join(parts, " / ")
}

type pricesBridge struct{ svc *pricingapp.Service }

// UnitPrices quotes each line at its own quantity so volume tiers apply.
func (b pricesBridge) UnitPrices(ctx context.Context, listCode string, c money.Currency, lines []ports.LineQty) (map[id.ID]money.Money, error) {
	byQty := map[int][]id.ID{}
	for _, l := range lines {
		byQty[l.Qty] = append(byQty[l.Qty], l.VariantID)
	}
	qtys := make([]int, 0, len(byQty))
	for q := range byQty {
		qtys = append(qtys, q)
	}
	sort.Ints(qtys)
	out := make(map[id.ID]money.Money, len(lines))
	for _, q := range qtys {
		quotes, err := b.svc.Quote(ctx, pricingapp.QuoteQuery{ListCode: listCode, Currency: c, VariantIDs: byQty[q], Qty: q})
		if err != nil {
			return nil, err
		}
		for vid, qt := range quotes {
			out[vid] = qt.Unit
		}
	}
	return out, nil
}

type stockBridge struct{ svc *inventoryapp.Service }

func (b stockBridge) Availability(ctx context.Context, ids []id.ID) (map[id.ID]int, error) {
	return b.svc.Availability(ctx, ids)
}

func (b stockBridge) Reserve(ctx context.Context, orderID id.ID, lines []ports.LineQty) error {
	ls := make([]inventorydomain.Line, len(lines))
	for i, l := range lines {
		ls[i] = inventorydomain.Line{VariantID: l.VariantID, Qty: l.Qty}
	}
	err := b.svc.Reserve(ctx, orderID, ls)
	if e, ok := apperr.As(err); ok && e.Code == "inventory.insufficient_stock" {
		// Same meaning, in the orders vocabulary the web translates.
		return apperr.Conflict("orders.item_insufficient_stock", e.Field, "not enough stock for some products")
	}
	return err
}

func (b stockBridge) Confirm(ctx context.Context, orderID id.ID) error {
	return b.svc.Confirm(ctx, orderID)
}
func (b stockBridge) Release(ctx context.Context, orderID id.ID) error {
	return b.svc.Release(ctx, orderID)
}
func (b stockBridge) Restock(ctx context.Context, orderID id.ID) error {
	return b.svc.Restock(ctx, orderID)
}

type shippingBridge struct{ svc *shippingapp.Service }

func (b shippingBridge) Quote(ctx context.Context, department string, pickup bool, weightG int) (string, money.Money, error) {
	zone, amount, err := b.svc.Quote(ctx, department, pickup, weightG)
	return string(zone), amount, err
}

type customersBridge struct{ svc *identityapp.Service }

func (b customersBridge) Info(ctx context.Context, accountID id.ID) (ports.CustomerInfo, error) {
	p, err := b.svc.Profile(ctx, accountID)
	if err != nil {
		return ports.CustomerInfo{}, err
	}
	info := ports.CustomerInfo{Email: p.Account.Email, Name: p.Account.DisplayName}
	if c := p.Customer; c != nil {
		info.PriceListCode = c.PriceListCode
		info.Billing = &ordersdomain.Billing{
			CustomerType: string(c.Type), DocumentType: string(c.DocumentType), DocumentNumber: c.DocumentNumber,
			BusinessName: c.BusinessName, InvoiceType: c.InvoiceType(),
		}
		if info.Billing.BusinessName == "" {
			info.Billing.BusinessName = p.Account.DisplayName
		}
	}
	for _, a := range p.Addresses {
		info.Addresses = append(info.Addresses, ports.Address{
			ID: a.ID, Recipient: a.Recipient, Line1: a.Line1, Reference: a.Reference,
			District: a.District, Province: a.Province, Department: a.Department, Phone: a.Phone,
		})
	}
	return info, nil
}
