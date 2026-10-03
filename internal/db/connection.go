// Package db opens the SQLite database and holds the generated queries.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/mizuchilabs/sqlite-schema-diff/pkg/diff"
)

//go:embed schemas/*.sql
var schemaFS embed.FS

// pragmas live in the DSN so every connection gets them, not just the first.
const pragmas = "_txlock=immediate" +
	"&_pragma=busy_timeout(5000)" +
	"&_pragma=journal_mode(WAL)" +
	"&_pragma=synchronous(NORMAL)" +
	"&_pragma=foreign_keys(ON)" +
	"&_pragma=temp_store(MEMORY)"

// Open connects to inkan.db inside dataDir and applies the schema. The pool
// is closed when ctx is cancelled.
func Open(ctx context.Context, dataDir string) (*Queries, error) {
	// Owner only, the database holds the signing keys.
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("creating data directory: %w", err)
	}

	path := filepath.Join(dataDir, "inkan.db")
	backup := ""
	if _, err := os.Stat(path); err == nil {
		backup = path + ".backup"
	}

	sqliteDB, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?"+pragmas)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	sqliteDB.SetMaxOpenConns(1)

	if err := migrate(ctx, sqliteDB, backup); err != nil {
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("applying schema: %w", err)
	}

	go func() {
		<-ctx.Done()
		_ = sqliteDB.Close()
	}()

	return New(sqliteDB), nil
}

// migrate applies the embedded schema. A backup is only written when a path is
// given and the schema actually changes.
func migrate(ctx context.Context, db *sql.DB, backupPath string) error {
	schemas, err := fs.Sub(schemaFS, "schemas")
	if err != nil {
		return err
	}
	_, err = diff.Apply(ctx, db, schemas, diff.ApplyOptions{BackupPath: backupPath})
	return err
}
