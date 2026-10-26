// Package mysql stores the catalog in MySQL. Aggregates are loaded in batches
// (one query per table for a whole page of products) to avoid N+1 queries.
package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/CodyLionVivo/duradent/services/api/internal/catalog/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/catalog/ports"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/locale"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/mysql"
)

type Products struct{ db *sql.DB }

func NewProducts(db *sql.DB) *Products { return &Products{db: db} }

var _ ports.ProductRepository = (*Products)(nil)

func (r *Products) Create(ctx context.Context, p *domain.Product) error {
	return r.save(ctx, p, true)
}

func (r *Products) Update(ctx context.Context, p *domain.Product) error {
	return r.save(ctx, p, false)
}

func (r *Products) save(ctx context.Context, p *domain.Product, insert bool) error {
	err := mysql.InTx(ctx, r.db, func(tx *sql.Tx) error {
		var category any
		if !p.CategoryID.IsNil() {
			category = p.CategoryID
		}
		if insert {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO products (id, slug, brand, status, category_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				p.ID, p.Slug, p.Brand, p.Status, category, p.CreatedAt, p.UpdatedAt); err != nil {
				return err
			}
		} else {
			res, err := tx.ExecContext(ctx,
				`UPDATE products SET slug = ?, brand = ?, status = ?, category_id = ?, updated_at = ? WHERE id = ?`,
				p.Slug, p.Brand, p.Status, category, p.UpdatedAt, p.ID)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				if exists, err := rowExists(ctx, tx, "products", p.ID); err != nil || !exists {
					return notFoundOr(err)
				}
			}
		}
		if err := saveTexts(ctx, tx, "product_translations", "product_id", p.ID, p.Texts); err != nil {
			return err
		}
		if err := saveVariants(ctx, tx, p); err != nil {
			return err
		}
		return saveMedia(ctx, tx, p)
	})
	return translateDuplicate(err)
}

func notFoundOr(err error) error {
	if err != nil {
		return err
	}
	return apperr.NotFound("catalog.product_not_found", "product not found")
}

func rowExists(ctx context.Context, tx *sql.Tx, table string, rowID id.ID) (bool, error) {
	var one int
	err := tx.QueryRowContext(ctx, "SELECT 1 FROM "+table+" WHERE id = ?", rowID).Scan(&one) //nolint:gosec // table is a constant
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func translateDuplicate(err error) error {
	key, ok := mysql.DuplicateKey(err)
	if !ok {
		return err
	}
	switch key {
	case "uk_products_slug", "uk_categories_slug":
		return apperr.Conflict("catalog.slug_taken", "slug", "slug already in use")
	case "uk_variants_sku":
		return apperr.Conflict("catalog.sku_taken", "sku", "SKU already used by another product")
	case "uk_attributes_code", "uk_attribute_values_code":
		return apperr.Conflict("catalog.code_taken", "code", "code already in use")
	}
	return apperr.Conflict("catalog.duplicate", "", "duplicate value")
}

func saveTexts(ctx context.Context, tx *sql.Tx, table, fk string, ownerID id.ID, texts map[locale.Locale]domain.Translation) error {
	//nolint:gosec // table and fk are constants from this package
	if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE "+fk+" = ?", ownerID); err != nil {
		return err
	}
	for l, t := range texts {
		//nolint:gosec // table and fk are constants from this package
		if _, err := tx.ExecContext(ctx, "INSERT INTO "+table+" ("+fk+", locale, name, description) VALUES (?, ?, ?, ?)",
			ownerID, string(l), t.Name, t.Description); err != nil {
			return err
		}
	}
	return nil
}

func saveVariants(ctx context.Context, tx *sql.Tx, p *domain.Product) error {
	valueIDs, err := attributeValueIDs(ctx, tx)
	if err != nil {
		return err
	}
	existing, err := queryIDs(ctx, tx, "SELECT id FROM variants WHERE product_id = ? FOR UPDATE", p.ID)
	if err != nil {
		return err
	}
	known := make(map[id.ID]bool, len(existing))
	for _, v := range existing {
		known[v] = true
	}
	// Plain INSERT/UPDATE, never ON DUPLICATE KEY: a SKU collision with
	// another product must fail, not overwrite that product's variant.
	for i, v := range p.Variants {
		if known[v.ID] {
			_, err = tx.ExecContext(ctx, `
				UPDATE variants SET sku = ?, barcode = ?, weight_g = ?, is_default = ?, sort_order = ?,
					archived_at = CASE WHEN ? THEN COALESCE(archived_at, ?) ELSE NULL END
				WHERE id = ? AND product_id = ?`,
				v.SKU, v.Barcode, v.WeightG, v.IsDefault, i, v.Archived, p.UpdatedAt, v.ID, p.ID)
		} else {
			var archived any
			if v.Archived {
				archived = p.UpdatedAt
			}
			_, err = tx.ExecContext(ctx, `
				INSERT INTO variants (id, product_id, sku, barcode, weight_g, is_default, archived_at, sort_order)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				v.ID, p.ID, v.SKU, v.Barcode, v.WeightG, v.IsDefault, archived, i)
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM variant_attribute_values WHERE variant_id = ?", v.ID); err != nil {
			return err
		}
		for attr, val := range v.Options {
			avID, ok := valueIDs[attr+"="+val]
			if !ok {
				return apperr.Invalid("catalog.attribute_value_unknown", "options."+attr, "unknown value "+val)
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO variant_attribute_values (variant_id, attribute_value_id) VALUES (?, ?)", v.ID, avID); err != nil {
				return err
			}
		}
	}
	return nil
}

func attributeValueIDs(ctx context.Context, tx *sql.Tx) (map[string]id.ID, error) {
	rows, err := tx.QueryContext(ctx, `SELECT av.id, a.code, av.code FROM attribute_values av JOIN attributes a ON a.id = av.attribute_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]id.ID{}
	for rows.Next() {
		var avID id.ID
		var attr, val string
		if err := rows.Scan(&avID, &attr, &val); err != nil {
			return nil, err
		}
		out[attr+"="+val] = avID
	}
	return out, rows.Err()
}

func saveMedia(ctx context.Context, tx *sql.Tx, p *domain.Product) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM media WHERE product_id = ?", p.ID); err != nil {
		return err
	}
	for _, m := range p.Media {
		alt, err := json.Marshal(nonNil(m.Alt))
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO media (id, product_id, s3_key, alt, sort_order) VALUES (?, ?, ?, ?, ?)",
			m.ID, p.ID, m.Key, alt, m.SortOrder); err != nil {
			return err
		}
	}
	return nil
}

func nonNil(t locale.Text) locale.Text {
	if t == nil {
		return locale.Text{}
	}
	return t
}

func (r *Products) ByID(ctx context.Context, productID id.ID) (*domain.Product, error) {
	return r.one(ctx, "p.id = ?", productID)
}

func (r *Products) BySlug(ctx context.Context, slug string) (*domain.Product, error) {
	return r.one(ctx, "p.slug = ?", slug)
}

func (r *Products) one(ctx context.Context, where string, arg any) (*domain.Product, error) {
	ps, err := r.load(ctx, "SELECT p.id FROM products p WHERE "+where, arg)
	if err != nil {
		return nil, err
	}
	if len(ps) == 0 {
		return nil, apperr.NotFound("catalog.product_not_found", "product not found")
	}
	return ps[0], nil
}

// sqlCond is one WHERE fragment with its bound arguments. Fragments are
// built from constant SQL; every value goes through a placeholder.
type sqlCond struct {
	sql  string
	args []any
}

func where(conds ...sqlCond) (string, []any) {
	var parts []string
	var args []any
	for _, c := range conds {
		if c.sql == "" {
			continue
		}
		parts = append(parts, c.sql)
		args = append(args, c.args...)
	}
	if len(parts) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(parts, " AND "), args
}

func inList(column string, values []any) sqlCond {
	if len(values) == 0 {
		return sqlCond{}
	}
	return sqlCond{column + " IN (" + mysql.Placeholders(len(values)) + ")", values}
}

// baseConds are status and text search: they always apply.
func baseConds(f ports.ProductFilter) []sqlCond {
	statuses := make([]any, len(f.Statuses))
	for i, s := range f.Statuses {
		statuses[i] = string(s)
	}
	conds := []sqlCond{inList("p.status", statuses)}
	if f.Query != "" {
		like := "%" + escapeLike(f.Query) + "%"
		conds = append(conds, sqlCond{`(EXISTS (SELECT 1 FROM product_translations q WHERE q.product_id = p.id AND q.locale IN (?, ?) AND q.name LIKE ?)
			OR EXISTS (SELECT 1 FROM variants qv WHERE qv.product_id = p.id AND qv.archived_at IS NULL AND qv.sku LIKE ?))`,
			[]any{string(localeOrDefault(f.Locale)), string(locale.Default), like, like}})
	}
	return conds
}

func categoryCond(f ports.ProductFilter) sqlCond {
	ids := make([]any, len(f.CategoryIDs))
	for i, c := range f.CategoryIDs {
		ids[i] = c
	}
	return inList("p.category_id", ids)
}

func brandCond(f ports.ProductFilter) sqlCond {
	brands := make([]any, len(f.Brands))
	for i, b := range f.Brands {
		brands[i] = b
	}
	return inList("p.brand", brands)
}

// attributeConds are the attribute selections as conditions on the variant
// aliased v, leaving out the attribute `skip` (its own facet).
func attributeConds(f ports.ProductFilter, skip string) []sqlCond {
	codes := make([]string, 0, len(f.Attributes))
	for code := range f.Attributes {
		codes = append(codes, code)
	}
	sort.Strings(codes) // stable SQL for the same filter
	var conds []sqlCond
	for _, code := range codes {
		values := f.Attributes[code]
		if code == skip || len(values) == 0 {
			continue
		}
		args := []any{code}
		for _, v := range values {
			args = append(args, v)
		}
		conds = append(conds, sqlCond{`EXISTS (SELECT 1 FROM variant_attribute_values fx
			JOIN attribute_values fav ON fav.id = fx.attribute_value_id
			JOIN attributes fa ON fa.id = fav.attribute_id
			WHERE fx.variant_id = v.id AND fa.code = ? AND fav.code IN (` + mysql.Placeholders(len(values)) + `))`, args})
	}
	return conds
}

// variantCond requires an active variant matching every attribute selection.
func variantCond(f ports.ProductFilter) sqlCond {
	conds := attributeConds(f, "")
	if len(conds) == 0 {
		return sqlCond{}
	}
	inner, args := where(conds...)
	return sqlCond{"EXISTS (SELECT 1 FROM variants v" + inner + " AND v.product_id = p.id AND v.archived_at IS NULL)", args}
}

func (r *Products) List(ctx context.Context, f ports.ProductFilter) ([]*domain.Product, int, error) {
	cond, args := where(append(baseConds(f), categoryCond(f), brandCond(f), variantCond(f))...)

	var total int
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM products p"+cond, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count products: %w", err)
	}

	order := "COALESCE(tl.name, td.name), p.id"
	if f.Sort == ports.SortNewest {
		order = "p.created_at DESC, p.id DESC"
	}
	l := localeOrDefault(f.Locale)
	//nolint:gosec // cond and order are built from constant fragments; values are bound
	q := `SELECT p.id FROM products p
		LEFT JOIN product_translations tl ON tl.product_id = p.id AND tl.locale = ?
		LEFT JOIN product_translations td ON td.product_id = p.id AND td.locale = ?` + cond +
		` ORDER BY ` + order + ` LIMIT ? OFFSET ?`
	all := append([]any{string(l), string(locale.Default)}, args...)
	all = append(all, f.Limit, f.Offset)
	ps, err := r.load(ctx, q, all...)
	return ps, total, err
}

func (r *Products) Facets(ctx context.Context, f ports.ProductFilter) (ports.Facets, error) {
	out := ports.Facets{Categories: map[id.ID]int{}, Attributes: map[string][]ports.FacetCount{}}
	base := baseConds(f)

	// Brands: everything but the brand selection.
	cond, args := where(append(base, categoryCond(f), variantCond(f), sqlCond{"p.brand <> ''", nil})...)
	//nolint:gosec // cond is built from constant fragments; values are bound
	rows, err := r.db.QueryContext(ctx, "SELECT p.brand, COUNT(*) FROM products p"+cond+" GROUP BY p.brand ORDER BY p.brand", args...)
	if err != nil {
		return out, fmt.Errorf("brand facets: %w", err)
	}
	if out.Brands, err = scanCounts(rows); err != nil {
		return out, err
	}

	// Categories: everything but the category selection.
	cond, args = where(append(base, brandCond(f), variantCond(f), sqlCond{"p.category_id IS NOT NULL", nil})...)
	//nolint:gosec // cond is built from constant fragments; values are bound
	if err := r.countInto(ctx, "SELECT p.category_id, COUNT(*) FROM products p"+cond+" GROUP BY p.category_id", args, out.Categories); err != nil {
		return out, fmt.Errorf("category facets: %w", err)
	}

	// Attributes: one query for the attributes nobody selected (all
	// selections apply) and one per selected attribute (its own selection
	// left out).
	var selected []string
	for code, values := range f.Attributes {
		if len(values) > 0 {
			selected = append(selected, code)
		}
	}
	sort.Strings(selected)
	groups := []struct {
		skip  string
		scope sqlCond
	}{{"", sqlCond{}}}
	if len(selected) > 0 {
		codes := make([]any, len(selected))
		for i, c := range selected {
			codes[i] = c
		}
		groups[0].scope = sqlCond{"a.code NOT IN (" + mysql.Placeholders(len(codes)) + ")", codes}
		for _, code := range selected {
			groups = append(groups, struct {
				skip  string
				scope sqlCond
			}{code, sqlCond{"a.code = ?", []any{code}}})
		}
	}
	for _, g := range groups {
		conds := append(append([]sqlCond{}, base...), categoryCond(f), brandCond(f), g.scope)
		conds = append(conds, attributeConds(f, g.skip)...)
		cond, args := where(conds...)
		//nolint:gosec // cond is built from constant fragments; values are bound
		rows, err := r.db.QueryContext(ctx, `SELECT a.code, av.code, COUNT(DISTINCT p.id) FROM products p
			JOIN variants v ON v.product_id = p.id AND v.archived_at IS NULL
			JOIN variant_attribute_values vav ON vav.variant_id = v.id
			JOIN attribute_values av ON av.id = vav.attribute_value_id
			JOIN attributes a ON a.id = av.attribute_id`+cond+` GROUP BY a.code, av.code`, args...)
		if err != nil {
			return out, fmt.Errorf("attribute facets: %w", err)
		}
		if err := scanAttributeCounts(rows, out.Attributes); err != nil {
			return out, err
		}
	}
	return out, nil
}

func scanCounts(rows *sql.Rows) ([]ports.FacetCount, error) {
	defer func() { _ = rows.Close() }()
	var out []ports.FacetCount
	for rows.Next() {
		var c ports.FacetCount
		if err := rows.Scan(&c.Value, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *Products) countInto(ctx context.Context, q string, args []any, dst map[id.ID]int) error {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var k id.ID
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return err
		}
		dst[k] = n
	}
	return rows.Err()
}

func scanAttributeCounts(rows *sql.Rows, dst map[string][]ports.FacetCount) error {
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var code string
		var c ports.FacetCount
		if err := rows.Scan(&code, &c.Value, &c.Count); err != nil {
			return err
		}
		dst[code] = append(dst[code], c)
	}
	return rows.Err()
}

func (r *Products) ByVariantIDs(ctx context.Context, variantIDs []id.ID) ([]*domain.Product, error) {
	if len(variantIDs) == 0 {
		return nil, nil
	}
	args := make([]any, len(variantIDs))
	for i, v := range variantIDs {
		args[i] = v
	}
	return r.load(ctx, "SELECT DISTINCT product_id FROM variants WHERE id IN ("+mysql.Placeholders(len(args))+")", args...)
}

func localeOrDefault(l locale.Locale) locale.Locale {
	if l == "" {
		return locale.Default
	}
	return l
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// load runs idQuery (which must select product ids, in display order) and
// hydrates the matching aggregates.
func (r *Products) load(ctx context.Context, idQuery string, args ...any) ([]*domain.Product, error) {
	ids, err := queryIDs(ctx, r.db, idQuery, args...)
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	in := mysql.Placeholders(len(ids))
	idArgs := make([]any, len(ids))
	for i, v := range ids {
		idArgs[i] = v
	}

	byID := make(map[id.ID]*domain.Product, len(ids))
	rows, err := r.db.QueryContext(ctx, //nolint:gosec // only placeholders are interpolated
		"SELECT id, slug, brand, status, category_id, created_at, updated_at FROM products WHERE id IN ("+in+")", idArgs...)
	if err != nil {
		return nil, err
	}
	err = eachRow(rows, func() error {
		p := &domain.Product{Texts: map[locale.Locale]domain.Translation{}}
		var category nullID
		if err := rows.Scan(&p.ID, &p.Slug, &p.Brand, &p.Status, &category, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return err
		}
		p.CategoryID = category.ID
		byID[p.ID] = p
		return nil
	})
	if err != nil {
		return nil, err
	}

	if err := loadTexts(ctx, r.db, "product_translations", "product_id", in, idArgs, func(owner id.ID, l locale.Locale, t domain.Translation) {
		if p := byID[owner]; p != nil {
			p.Texts[l] = t
		}
	}); err != nil {
		return nil, err
	}
	if err := r.loadVariants(ctx, in, idArgs, byID); err != nil {
		return nil, err
	}
	if err := r.loadMedia(ctx, in, idArgs, byID); err != nil {
		return nil, err
	}

	out := make([]*domain.Product, 0, len(ids))
	for _, pid := range ids {
		if p := byID[pid]; p != nil {
			out = append(out, p)
		}
	}
	return out, nil
}

func (r *Products) loadVariants(ctx context.Context, in string, idArgs []any, byID map[id.ID]*domain.Product) error {
	rows, err := r.db.QueryContext(ctx, //nolint:gosec // only placeholders are interpolated
		`SELECT id, product_id, sku, barcode, weight_g, is_default, archived_at IS NOT NULL
		 FROM variants WHERE product_id IN (`+in+`) ORDER BY product_id, sort_order`, idArgs...)
	if err != nil {
		return err
	}
	index := map[id.ID]*domain.Variant{}
	order := map[id.ID][]id.ID{}
	err = eachRow(rows, func() error {
		v := &domain.Variant{Options: map[string]string{}}
		var pid id.ID
		if err := rows.Scan(&v.ID, &pid, &v.SKU, &v.Barcode, &v.WeightG, &v.IsDefault, &v.Archived); err != nil {
			return err
		}
		index[v.ID] = v
		order[pid] = append(order[pid], v.ID)
		return nil
	})
	if err != nil {
		return err
	}

	rows, err = r.db.QueryContext(ctx, //nolint:gosec // only placeholders are interpolated
		`SELECT vav.variant_id, a.code, av.code FROM variant_attribute_values vav
		 JOIN variants v ON v.id = vav.variant_id
		 JOIN attribute_values av ON av.id = vav.attribute_value_id
		 JOIN attributes a ON a.id = av.attribute_id
		 WHERE v.product_id IN (`+in+`)`, idArgs...)
	if err != nil {
		return err
	}
	err = eachRow(rows, func() error {
		var vid id.ID
		var attr, val string
		if err := rows.Scan(&vid, &attr, &val); err != nil {
			return err
		}
		if v := index[vid]; v != nil {
			v.Options[attr] = val
		}
		return nil
	})
	if err != nil {
		return err
	}
	for pid, vids := range order {
		p := byID[pid]
		if p == nil {
			continue
		}
		for _, vid := range vids {
			p.Variants = append(p.Variants, *index[vid])
		}
	}
	return nil
}

func (r *Products) loadMedia(ctx context.Context, in string, idArgs []any, byID map[id.ID]*domain.Product) error {
	rows, err := r.db.QueryContext(ctx, //nolint:gosec // only placeholders are interpolated
		"SELECT id, product_id, s3_key, alt, sort_order FROM media WHERE product_id IN ("+in+") ORDER BY product_id, sort_order", idArgs...)
	if err != nil {
		return err
	}
	return eachRow(rows, func() error {
		var m domain.Media
		var pid id.ID
		var alt []byte
		if err := rows.Scan(&m.ID, &pid, &m.Key, &alt, &m.SortOrder); err != nil {
			return err
		}
		if err := json.Unmarshal(alt, &m.Alt); err != nil {
			return fmt.Errorf("media alt: %w", err)
		}
		if p := byID[pid]; p != nil {
			p.Media = append(p.Media, m)
		}
		return nil
	})
}

// --- shared helpers ---------------------------------------------------------------

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func queryIDs(ctx context.Context, q queryer, query string, args ...any) ([]id.ID, error) {
	rows, err := q.QueryContext(ctx, query, args...) //nolint:sqlclosecheck // closed by eachRow
	if err != nil {
		return nil, err
	}
	var ids []id.ID
	err = eachRow(rows, func() error {
		var v id.ID
		if err := rows.Scan(&v); err != nil {
			return err
		}
		ids = append(ids, v)
		return nil
	})
	return ids, err
}

func eachRow(rows *sql.Rows, fn func() error) error {
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := fn(); err != nil {
			return err
		}
	}
	return rows.Err()
}

func loadTexts(ctx context.Context, q queryer, table, fk, in string, args []any, set func(id.ID, locale.Locale, domain.Translation)) error {
	//nolint:gosec // table and fk are constants; only placeholders are interpolated
	rows, err := q.QueryContext(ctx, "SELECT "+fk+", locale, name, description FROM "+table+" WHERE "+fk+" IN ("+in+")", args...)
	if err != nil {
		return err
	}
	return eachRow(rows, func() error {
		var owner id.ID
		var l string
		var t domain.Translation
		if err := rows.Scan(&owner, &l, &t.Name, &t.Description); err != nil {
			return err
		}
		set(owner, locale.Locale(l), t)
		return nil
	})
}

// nullID scans a nullable BINARY(16) column.
type nullID struct{ ID id.ID }

func (n *nullID) Scan(src any) error {
	if src == nil {
		n.ID = id.Nil
		return nil
	}
	return n.ID.Scan(src)
}
