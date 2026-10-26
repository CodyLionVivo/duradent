// Package mysql opens the shared connection pool and runs schema migrations.
package mysql

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	drv "github.com/go-sql-driver/mysql"
	"github.com/golang-migrate/migrate/v4"
	migratemysql "github.com/golang-migrate/migrate/v4/database/mysql"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// TLSConfigName is the DSN value (tls=rds) that selects the CA registered by
// RegisterCA.
const TLSConfigName = "rds"

// RegisterCA trusts the PEM bundle at path for MySQL connections whose DSN
// says tls=rds. RDS certificates are signed by Amazon's own CA, which is not
// in the system pool; the image ships its bundle.
func RegisterCA(path string) error {
	pem, err := os.ReadFile(path) //nolint:gosec // operator-set path (MYSQL_CA_FILE), not user input
	if err != nil {
		return fmt.Errorf("mysql CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return fmt.Errorf("mysql CA: no certificates in %s", path)
	}
	return drv.RegisterTLSConfig(TLSConfigName, &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12})
}

// Open creates a pool and verifies connectivity, retrying while MySQL starts up.
func Open(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)

	var pingErr error
	for attempt := 0; attempt < 30; attempt++ {
		if pingErr = db.PingContext(ctx); pingErr == nil {
			return db, nil
		}
		select {
		case <-ctx.Done():
			_ = db.Close()
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	_ = db.Close()
	return nil, fmt.Errorf("ping mysql: %w", pingErr)
}

// Migrate applies every pending migration from fsys (a directory of
// NNNN_name.up.sql / .down.sql files). The DSN must allow multiStatements.
func Migrate(db *sql.DB, fsys fs.FS, dir string) error {
	src, err := iofs.New(fsys, dir)
	if err != nil {
		return fmt.Errorf("migration source: %w", err)
	}
	drv, err := migratemysql.WithInstance(db, &migratemysql.Config{})
	if err != nil {
		return fmt.Errorf("migration driver: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "mysql", drv)
	if err != nil {
		return fmt.Errorf("migrate init: %w", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}
