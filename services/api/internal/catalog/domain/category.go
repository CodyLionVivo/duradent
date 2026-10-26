package domain

import (
	"strings"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/locale"
)

// Category groups products for navigation: Brackets › Metálicos, Alambres › NiTi…
type Category struct {
	ID        id.ID
	Slug      string
	ParentID  id.ID // id.Nil for a top-level category
	SortOrder int
	Texts     map[locale.Locale]Translation
	CreatedAt time.Time
	UpdatedAt time.Time
}

type CategoryDetails struct {
	Slug      string
	ParentID  id.ID
	SortOrder int
	Texts     map[locale.Locale]Translation
}

func NewCategory(d CategoryDetails, now time.Time) (*Category, error) {
	c := &Category{ID: id.New(), CreatedAt: now}
	if err := c.Update(d, now); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Category) Update(d CategoryDetails, now time.Time) error {
	d.Slug = strings.TrimSpace(d.Slug)
	if !ValidSlug(d.Slug) {
		return apperr.Invalid("catalog.slug_invalid", "slug", "use lowercase letters, digits and hyphens")
	}
	if d.ParentID == c.ID {
		return apperr.Invalid("catalog.category_parent_self", "parent_id", "a category cannot be its own parent")
	}
	texts, err := cleanTexts(d.Texts)
	if err != nil {
		return err
	}
	c.Slug, c.ParentID, c.SortOrder, c.Texts = d.Slug, d.ParentID, d.SortOrder, texts
	c.UpdatedAt = now
	return nil
}

func (c *Category) Name(l locale.Locale) string {
	if t, ok := c.Texts[l]; ok && t.Name != "" {
		return t.Name
	}
	return c.Texts[locale.Default].Name
}

// Descendants returns root and every category below it. It stops on cycles,
// which the application layer also prevents when saving.
func Descendants(all []*Category, root id.ID) []id.ID {
	children := make(map[id.ID][]id.ID, len(all))
	for _, c := range all {
		children[c.ParentID] = append(children[c.ParentID], c.ID)
	}
	out := []id.ID{root}
	seen := map[id.ID]bool{root: true}
	for i := 0; i < len(out); i++ {
		for _, ch := range children[out[i]] {
			if !seen[ch] {
				seen[ch] = true
				out = append(out, ch)
			}
		}
	}
	return out
}

// WouldCycle reports whether making parent the parent of child creates a loop.
func WouldCycle(all []*Category, child, parent id.ID) bool {
	if parent.IsNil() {
		return false
	}
	for _, d := range Descendants(all, child) {
		if d == parent {
			return true
		}
	}
	return false
}
