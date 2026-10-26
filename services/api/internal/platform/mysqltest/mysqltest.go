// Package mysqltest starts a throwaway MySQL 8 container for adapter
// integration tests. Tests that use it are skipped under `go test -short`.
package mysqltest

import (
	"context"
	"database/sql"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	tcmysql "github.com/testcontainers/testcontainers-go/modules/mysql"

	"github.com/CodyLionVivo/duradent/services/api/internal/platform/mysql"
)

const image = "mysql:8.4"

// Start returns a connection to an empty database in a fresh container that
// is terminated when the test ends.
func Start(t *testing.T) *sql.DB {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Docker")
	}
	ctx := context.Background()

	ctr, err := tcmysql.Run(ctx, image,
		tcmysql.WithDatabase("duradent"),
		tcmysql.WithUsername("duradent"),
		tcmysql.WithPassword("duradent"),
	)
	testcontainers.CleanupContainer(t, ctr)
	if err != nil {
		t.Fatalf("start mysql container: %v", err)
	}

	dsn, err := ctr.ConnectionString(ctx, "parseTime=true", "multiStatements=true")
	if err != nil {
		t.Fatal(err)
	}
	db, err := mysql.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
