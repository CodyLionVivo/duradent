package mysql_test

import (
	"context"
	"testing"

	"github.com/CodyLionVivo/duradent/services/api/internal/platform/mysql"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/mysqltest"
	"github.com/CodyLionVivo/duradent/services/api/migrations"
)

func TestMigrateCreatesPlatformTablesAndIsRepeatable(t *testing.T) {
	db := mysqltest.Start(t)

	if err := mysql.Migrate(db, migrations.FS, "."); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if err := mysql.Migrate(db, migrations.FS, "."); err != nil {
		t.Fatalf("second migrate should be a no-op: %v", err)
	}

	for _, table := range []string{"outbox", "processed_messages", "idempotency_keys"} {
		var n int
		err := db.QueryRowContext(context.Background(),
			"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?",
			table).Scan(&n)
		if err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("table %s not created", table)
		}
	}
}

func TestProcessedMessagesRejectsDuplicates(t *testing.T) {
	db := mysqltest.Start(t)
	if err := mysql.Migrate(db, migrations.FS, "."); err != nil {
		t.Fatal(err)
	}

	const insert = "INSERT INTO processed_messages (consumer, message_id, processed_at) VALUES ('inventory', UNHEX('0190F0A1B2C37D8E9F00112233445566'), NOW(6))"
	if _, err := db.Exec(insert); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if _, err := db.Exec(insert); err == nil {
		t.Fatal("expected duplicate key error: consumer idempotency relies on it")
	}
}
