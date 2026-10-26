package mysql

import (
	"context"
	"database/sql"
)

// Querier is what repositories need from either a *sql.DB or a *sql.Tx.
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type txKey struct{}

// Conn returns the transaction carried by ctx, or db when there is none.
// Repositories that use it take part in a unit of work started by another
// bounded context (e.g. placing an order reserves stock in the same
// transaction) without knowing about it.
func Conn(ctx context.Context, db *sql.DB) Querier {
	if tx, ok := ctx.Value(txKey{}).(*sql.Tx); ok {
		return tx
	}
	return db
}

// UnitOfWork runs functions in one database transaction propagated via ctx.
type UnitOfWork struct{ db *sql.DB }

func NewUnitOfWork(db *sql.DB) *UnitOfWork { return &UnitOfWork{db: db} }

// InTx runs fn in a transaction. Nested calls join the outer transaction.
func (u *UnitOfWork) InTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := ctx.Value(txKey{}).(*sql.Tx); ok {
		return fn(ctx)
	}
	return InTx(ctx, u.db, func(tx *sql.Tx) error {
		return fn(context.WithValue(ctx, txKey{}, tx))
	})
}
