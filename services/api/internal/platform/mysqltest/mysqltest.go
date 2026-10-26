// Package mysqltest gives adapter integration tests a real MySQL 8. One
// container is started per test binary (package) and every test gets its own
// empty database in it, so tests stay isolated without paying ~15s each.
// Testcontainers' reaper removes the container when the test process exits.
// Tests that use it are skipped under `go test -short`.
package mysqltest

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	tcmysql "github.com/testcontainers/testcontainers-go/modules/mysql"

	"github.com/CodyLionVivo/duradent/services/api/internal/platform/mysql"
)

const image = "mysql:8.4"

var (
	once    sync.Once
	rootDSN string // DSN template with a %s placeholder for the database name
	initErr error
	counter atomic.Int64
)

func start() {
	ctx := context.Background()
	ctr, err := tcmysql.Run(ctx, image,
		tcmysql.WithDatabase("bootstrap"),
		tcmysql.WithUsername("root"),
		tcmysql.WithPassword("test"),
		testcontainers.WithCmdArgs("--skip-log-bin", "--innodb-flush-log-at-trx-commit=0"),
	)
	if err != nil {
		initErr = fmt.Errorf("start mysql container: %w", err)
		return
	}
	dsn, err := ctr.ConnectionString(ctx, "parseTime=true", "multiStatements=true")
	if err != nil {
		initErr = err
		return
	}
	// dsn looks like root:test@tcp(host:port)/bootstrap?params
	slash := strings.LastIndex(dsn, "/bootstrap")
	rootDSN = dsn[:slash] + "/%s" + dsn[slash+len("/bootstrap"):]
}

// Start returns a connection to a new, empty database.
func Start(t *testing.T) *sql.DB {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Docker")
	}
	once.Do(start)
	if initErr != nil {
		t.Fatal(initErr)
	}
	ctx := context.Background()

	admin, err := mysql.Open(ctx, fmt.Sprintf(rootDSN, "bootstrap"))
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("t%d", counter.Add(1))
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	_ = admin.Close()

	db, err := mysql.Open(ctx, fmt.Sprintf(rootDSN, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
