package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	drv "github.com/go-sql-driver/mysql"
)

// InTx runs fn inside a transaction, committing when it returns nil.
func InTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// DuplicateKey reports whether err is a unique-key violation and, if so,
// which index was violated (e.g. "uk_products_slug").
func DuplicateKey(err error) (key string, ok bool) {
	var me *drv.MySQLError
	if !errors.As(err, &me) || me.Number != 1062 {
		return "", false
	}
	// Message: Duplicate entry 'x' for key 'products.uk_products_slug'
	msg := me.Message
	i := strings.LastIndex(msg, "for key '")
	if i < 0 {
		return "", true
	}
	key = strings.TrimSuffix(msg[i+len("for key '"):], "'")
	if dot := strings.LastIndex(key, "."); dot >= 0 {
		key = key[dot+1:]
	}
	return key, true
}

// Placeholders returns "?, ?, ?" for n arguments.
func Placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}
