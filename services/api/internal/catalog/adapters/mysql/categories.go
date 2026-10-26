package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/CodyLionVivo/duradent/services/api/internal/catalog/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/catalog/ports"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/locale"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/mysql"
)

type Categories struct{ db *sql.DB }

func NewCategories(db *sql.DB) *Categories { return &Categories{db: db} }

var _ ports.CategoryRepository = (*Categories)(nil)

func (r *Categories) Create(ctx context.Context, c *domain.Category) error {
	return translateDuplicate(mysql.InTx(ctx, r.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO categories (id, slug, parent_id, sort_order, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
			c.ID, c.Slug, optionalID(c.ParentID), c.SortOrder, c.CreatedAt, c.UpdatedAt); err != nil {
			return err
		}
		return saveTexts(ctx, tx, "category_translations", "category_id", c.ID, c.Texts)
	}))
}

func (r *Categories) Update(ctx context.Context, c *domain.Category) error {
	return translateDuplicate(mysql.InTx(ctx, r.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			"UPDATE categories SET slug = ?, parent_id = ?, sort_order = ?, updated_at = ? WHERE id = ?",
			c.Slug, optionalID(c.ParentID), c.SortOrder, c.UpdatedAt, c.ID); err != nil {
			return err
		}
		return saveTexts(ctx, tx, "category_translations", "category_id", c.ID, c.Texts)
	}))
}

func optionalID(v id.ID) any {
	if v.IsNil() {
		return nil
	}
	return v
}

func (r *Categories) ByID(ctx context.Context, categoryID id.ID) (*domain.Category, error) {
	return r.find(ctx, func(c *domain.Category) bool { return c.ID == categoryID })
}

func (r *Categories) BySlug(ctx context.Context, slug string) (*domain.Category, error) {
	return r.find(ctx, func(c *domain.Category) bool { return c.Slug == slug })
}

// find scans the full list: the category tree is small (tens of rows) and the
// storefront needs it whole for navigation anyway.
func (r *Categories) find(ctx context.Context, match func(*domain.Category) bool) (*domain.Category, error) {
	all, err := r.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range all {
		if match(c) {
			return c, nil
		}
	}
	return nil, apperr.NotFound("catalog.category_not_found", "category not found")
}

func (r *Categories) List(ctx context.Context) ([]*domain.Category, error) {
	rows, err := r.db.QueryContext(ctx,
		"SELECT id, slug, parent_id, sort_order, created_at, updated_at FROM categories ORDER BY sort_order, slug")
	if err != nil {
		return nil, err
	}
	var out []*domain.Category
	byID := map[id.ID]*domain.Category{}
	err = eachRow(rows, func() error {
		c := &domain.Category{Texts: map[locale.Locale]domain.Translation{}}
		var parent nullID
		if err := rows.Scan(&c.ID, &c.Slug, &parent, &c.SortOrder, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return err
		}
		c.ParentID = parent.ID
		out = append(out, c)
		byID[c.ID] = c
		return nil
	})
	if err != nil || len(out) == 0 {
		return out, err
	}
	rows, err = r.db.QueryContext(ctx, "SELECT category_id, locale, name, description FROM category_translations")
	if err != nil {
		return nil, err
	}
	err = eachRow(rows, func() error {
		var owner id.ID
		var l string
		var t domain.Translation
		if err := rows.Scan(&owner, &l, &t.Name, &t.Description); err != nil {
			return err
		}
		if c := byID[owner]; c != nil {
			c.Texts[locale.Locale(l)] = t
		}
		return nil
	})
	return out, err
}

// --- attributes -----------------------------------------------------------------

type Attributes struct{ db *sql.DB }

func NewAttributes(db *sql.DB) *Attributes { return &Attributes{db: db} }

var _ ports.AttributeRepository = (*Attributes)(nil)

func (r *Attributes) Create(ctx context.Context, a *domain.Attribute) error {
	return translateDuplicate(mysql.InTx(ctx, r.db, func(tx *sql.Tx) error {
		names, err := json.Marshal(nonNil(a.Names))
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO attributes (id, code, names, sort_order) VALUES (?, ?, ?, ?)",
			a.ID, a.Code, names, a.SortOrder); err != nil {
			return err
		}
		return saveValues(ctx, tx, a)
	}))
}

func (r *Attributes) Update(ctx context.Context, a *domain.Attribute) error {
	return translateDuplicate(mysql.InTx(ctx, r.db, func(tx *sql.Tx) error {
		names, err := json.Marshal(nonNil(a.Names))
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE attributes SET names = ?, sort_order = ? WHERE id = ?", names, a.SortOrder, a.ID); err != nil {
			return err
		}
		return saveValues(ctx, tx, a)
	}))
}

// saveValues upserts by primary key. Values are never deleted here: variants
// reference them.
func saveValues(ctx context.Context, tx *sql.Tx, a *domain.Attribute) error {
	for _, v := range a.Values {
		labels, err := json.Marshal(nonNil(v.Labels))
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO attribute_values (id, attribute_id, code, labels, sort_order) VALUES (?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE labels = IF(id = VALUES(id), VALUES(labels), labels),
				sort_order = IF(id = VALUES(id), VALUES(sort_order), sort_order)`,
			v.ID, a.ID, v.Code, labels, v.SortOrder); err != nil {
			return err
		}
	}
	return nil
}

func (r *Attributes) ByCode(ctx context.Context, code string) (*domain.Attribute, error) {
	all, err := r.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, a := range all {
		if a.Code == code {
			return a, nil
		}
	}
	return nil, apperr.NotFound("catalog.attribute_not_found", "attribute not found")
}

func (r *Attributes) List(ctx context.Context) ([]*domain.Attribute, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT id, code, names, sort_order FROM attributes ORDER BY sort_order, code")
	if err != nil {
		return nil, err
	}
	var out []*domain.Attribute
	byID := map[id.ID]*domain.Attribute{}
	err = eachRow(rows, func() error {
		a := &domain.Attribute{}
		var names []byte
		if err := rows.Scan(&a.ID, &a.Code, &names, &a.SortOrder); err != nil {
			return err
		}
		if err := json.Unmarshal(names, &a.Names); err != nil {
			return fmt.Errorf("attribute names: %w", err)
		}
		out = append(out, a)
		byID[a.ID] = a
		return nil
	})
	if err != nil || len(out) == 0 {
		return out, err
	}
	rows, err = r.db.QueryContext(ctx, "SELECT id, attribute_id, code, labels, sort_order FROM attribute_values ORDER BY sort_order, code")
	if err != nil {
		return nil, err
	}
	err = eachRow(rows, func() error {
		var v domain.AttributeValue
		var attrID id.ID
		var labels []byte
		if err := rows.Scan(&v.ID, &attrID, &v.Code, &labels, &v.SortOrder); err != nil {
			return err
		}
		if err := json.Unmarshal(labels, &v.Labels); err != nil {
			return fmt.Errorf("attribute value labels: %w", err)
		}
		if a := byID[attrID]; a != nil {
			a.Values = append(a.Values, v)
		}
		return nil
	})
	return out, err
}
