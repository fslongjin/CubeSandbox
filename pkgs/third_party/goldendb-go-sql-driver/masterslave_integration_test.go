package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"
)

// TestMasterSlaveIntegration needs a live GoldenDB cluster. Set
// GOLDENDB_TEST_DSN to run it; without it the test skips so the vendored
// driver stays runnable in CI.
func TestMasterSlaveIntegration(t *testing.T) {
	dsn := os.Getenv("GOLDENDB_TEST_DSN")
	if dsn == "" {
		t.Skip("GOLDENDB_TEST_DSN is not set")
	}

	db, err := sql.Open(DriverName, dsn)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	db.SetConnMaxLifetime(30 * time.Second)
	db.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err = db.PingContext(ctx)
	if err != nil {
		t.Fatalf("Ping failed: %v", err)
	}

	fmt.Println("=== Connection successful ===")

	rows, err := db.QueryContext(ctx, "SHOW MASTER STATUS")
	if err != nil {
		t.Fatalf("SHOW MASTER STATUS failed: %v", err)
	}
	defer rows.Close()

	fmt.Printf("\n=== SHOW MASTER STATUS ===\n")
	fmt.Printf("%-25s %-15s %-15s %-15s %-40s\n", "File", "Position", "Binlog_Do_DB", "Binlog_Ignore_DB", "Executed_Gtid_Set")

	for rows.Next() {
		var file, position, binlogDoDB, binlogIgnoreDB, gtidSet string
		if err := rows.Scan(&file, &position, &binlogDoDB, &binlogIgnoreDB, &gtidSet); err != nil {
			t.Fatalf("rows.Scan failed: %v", err)
		}
		fmt.Printf("%-25s %-15s %-15s %-15s %-40s\n", file, position, binlogDoDB, binlogIgnoreDB, gtidSet)
	}

	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	fmt.Printf("==========================\n")
}
