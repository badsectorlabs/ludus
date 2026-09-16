package ludusapi

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
)

func TestPocketBaseConnectionsWaitForSQLiteWriter(t *testing.T) {
	dataDir := t.TempDir()
	first := pocketbase.NewWithConfig(pocketbase.Config{
		HideStartBanner: true,
		DefaultDataDir:  dataDir,
		DBConnect:       connectLudusSQLite,
	})
	second := pocketbase.NewWithConfig(pocketbase.Config{
		HideStartBanner: true,
		DefaultDataDir:  dataDir,
		DBConnect:       connectLudusSQLite,
	})
	if err := first.Bootstrap(); err != nil {
		t.Fatalf("bootstrap first PocketBase connection: %v", err)
	}
	t.Cleanup(func() {
		if err := first.ResetBootstrapState(); err != nil {
			t.Errorf("close first PocketBase connection: %v", err)
		}
	})
	if err := second.Bootstrap(); err != nil {
		t.Fatalf("bootstrap second PocketBase connection: %v", err)
	}
	t.Cleanup(func() {
		if err := second.ResetBootstrapState(); err != nil {
			t.Errorf("close second PocketBase connection: %v", err)
		}
	})

	firstDB, ok := first.NonconcurrentDB().(*dbx.DB)
	if !ok {
		t.Fatalf("first PocketBase writer has type %T, want *dbx.DB", first.NonconcurrentDB())
	}
	secondDB, ok := second.NonconcurrentDB().(*dbx.DB)
	if !ok {
		t.Fatalf("second PocketBase writer has type %T, want *dbx.DB", second.NonconcurrentDB())
	}

	var busyTimeout int
	if err := secondDB.DB().QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("read SQLite busy timeout: %v", err)
	}
	if busyTimeout < 5000 {
		t.Fatalf("SQLite busy timeout = %dms, want at least 5000ms", busyTimeout)
	}

	var journalMode string
	if err := secondDB.DB().QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("read SQLite journal mode: %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("SQLite journal mode = %q, want %q", journalMode, "wal")
	}

	if _, err := firstDB.DB().Exec(`
		CREATE TABLE IF NOT EXISTS contention_test (
			id INTEGER PRIMARY KEY,
			value TEXT NOT NULL
		)
	`); err != nil {
		t.Fatalf("create contention table in %s: %v", filepath.Join(dataDir, "data.db"), err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	transaction, err := firstDB.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("start writer transaction: %v", err)
	}
	if _, err := transaction.ExecContext(ctx, "INSERT INTO contention_test(value) VALUES (?)", "first"); err != nil {
		transaction.Rollback()
		t.Fatalf("acquire first write lock: %v", err)
	}

	result := make(chan error, 1)
	startedAt := time.Now()
	go func() {
		_, execErr := secondDB.DB().ExecContext(ctx, "INSERT INTO contention_test(value) VALUES (?)", "second")
		result <- execErr
	}()

	select {
	case err := <-result:
		transaction.Rollback()
		t.Fatalf("second writer returned immediately instead of waiting for the lock: %v", err)
	case <-time.After(200 * time.Millisecond):
	}

	if err := transaction.Commit(); err != nil {
		t.Fatalf("commit first writer: %v", err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("second writer failed after the lock was released: %v", err)
		}
		if elapsed := time.Since(startedAt); elapsed < 200*time.Millisecond {
			t.Fatalf("second writer waited %s, want at least 200ms", elapsed)
		}
	case <-ctx.Done():
		t.Fatalf("second writer did not complete after the lock was released: %v", ctx.Err())
	}
}
