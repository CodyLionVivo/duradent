// Package domain models buying: the cart, orders with their price snapshot
// and lifecycle, and the payments that settle them.
package domain

import (
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
)

const (
	maxCartLines = 100
	MaxLineQty   = 9999
)

// Cart belongs either to a signed-in account or to an anonymous visitor
// identified by a random key kept in a cookie.
type Cart struct {
	ID        id.ID
	AccountID id.ID  // id.Nil for anonymous carts
	Key       string // random token for anonymous carts
	Items     []CartItem
	UpdatedAt time.Time
}

type CartItem struct {
	VariantID id.ID
	Qty       int
}

func NewCart(accountID id.ID, key string, now time.Time) *Cart {
	return &Cart{ID: id.New(), AccountID: accountID, Key: key, UpdatedAt: now}
}

func checkQty(qty int) error {
	if qty < 0 || qty > MaxLineQty {
		return apperr.Invalid("orders.qty_invalid", "qty", "quantity must be between 1 and 9999")
	}
	return nil
}

// Add increases a line (creating it if needed).
func (c *Cart) Add(variantID id.ID, qty int, now time.Time) error {
	if qty <= 0 {
		return apperr.Invalid("orders.qty_invalid", "qty", "quantity must be between 1 and 9999")
	}
	for i := range c.Items {
		if c.Items[i].VariantID == variantID {
			return c.Set(variantID, c.Items[i].Qty+qty, now)
		}
	}
	return c.Set(variantID, qty, now)
}

// Set replaces a line's quantity; zero removes it.
func (c *Cart) Set(variantID id.ID, qty int, now time.Time) error {
	if err := checkQty(qty); err != nil {
		return err
	}
	for i := range c.Items {
		if c.Items[i].VariantID == variantID {
			if qty == 0 {
				c.Items = append(c.Items[:i], c.Items[i+1:]...)
			} else {
				c.Items[i].Qty = qty
			}
			c.UpdatedAt = now
			return nil
		}
	}
	if qty == 0 {
		return nil
	}
	if len(c.Items) >= maxCartLines {
		return apperr.Invalid("orders.cart_full", "items", "a cart can hold at most 100 different products")
	}
	c.Items = append(c.Items, CartItem{VariantID: variantID, Qty: qty})
	c.UpdatedAt = now
	return nil
}

// Merge moves an anonymous cart's lines into this one (sign-in). Quantities
// add up, capped at the line maximum.
func (c *Cart) Merge(other *Cart, now time.Time) {
	for _, it := range other.Items {
		qty := it.Qty
		for _, mine := range c.Items {
			if mine.VariantID == it.VariantID {
				qty += mine.Qty
			}
		}
		_ = c.Set(it.VariantID, min(qty, MaxLineQty), now)
	}
}

func (c *Cart) Clear(now time.Time) {
	c.Items = nil
	c.UpdatedAt = now
}

func (c *Cart) Empty() bool { return len(c.Items) == 0 }
