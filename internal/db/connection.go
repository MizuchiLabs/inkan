// Package db opens the SQLite database and holds the generated queries.
package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/mizuchilabs/sqlite-schema-diff/pkg/diff"
)

// A bad edit or a rotation is often noticed late.
const backupsKept = 30

//go:embed schemas/*.sql
var schemaFS embed.FS

// pragmas live in the DSN so every connection gets them, not just the first.
const pragmas = "_txlock=immediate" +
	"&_pragma=busy_timeout(5000)" +
	"&_pragma=journal_mode(WAL)" +
	"&_pragma=synchronous(NORMAL)" +
	"&_pragma=foreign_keys(ON)" +
	"&_pragma=temp_store(MEMORY)"

// Open connects to inkan.db inside dataDir and applies the schema. Run the
// returned func before the process ends. Until then the last writes sit in the
// journal next to inkan.db, and a copy put in its place would meet them.
func Open(ctx context.Context, dataDir string) (*Queries, func(), error) {
	// Owner only, the database holds the signing keys.
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("creating data directory: %w", err)
	}

	path := filepath.Join(dataDir, "inkan.db")
	backup := ""
	if _, err := os.Stat(path); err == nil {
		backup = backupName(path)
	}

	// The directory may have been there before, with a mode of its own. The
	// file is owner only by itself then, and SQLite gives its journal the same.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("opening database: %w", err)
	}
	if err := errors.Join(f.Chmod(0o600), f.Close()); err != nil {
		slog.Warn("The database could not be made owner only, it holds the signing keys", "path", path, "error", err)
	}

	sqliteDB, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?"+pragmas)
	if err != nil {
		return nil, nil, fmt.Errorf("opening database: %w", err)
	}
	sqliteDB.SetMaxOpenConns(1)

	if err := migrate(ctx, sqliteDB, backup); err != nil {
		_ = sqliteDB.Close()
		return nil, nil, fmt.Errorf("applying schema: %w", err)
	}

	return New(sqliteDB), func() {
		if err := sqliteDB.Close(); err != nil {
			slog.Warn("The database could not be closed, its journal stays next to it", "path", path, "error", err)
		}
	}, nil
}

// OpenExisting is Open for a command that reads what inkan stored. A wrong
// data directory is an error there, not a new empty database.
func OpenExisting(ctx context.Context, dataDir string) (*Queries, func(), error) {
	if _, err := os.Stat(filepath.Join(dataDir, "inkan.db")); err != nil {
		return nil, nil, fmt.Errorf("no database in %s: %w", dataDir, err)
	}
	return Open(ctx, dataDir)
}

// Backup writes a copy of inkan.db inside dataDir, also while inkan runs. The
// copy goes to out, or next to the database under a dated name when out is
// empty. Backup returns where it is.
func Backup(ctx context.Context, dataDir, out string) (string, error) {
	path := filepath.Join(dataDir, "inkan.db")
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("no database to back up: %w", err)
	}
	if out == "" {
		out = backupName(path)
	}
	// Owner only like the data directory, the copy holds the signing keys too.
	f, err := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("creating backup: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("creating backup: %w", err)
	}

	sqliteDB, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?"+pragmas)
	if err != nil {
		return "", fmt.Errorf("opening database: %w", err)
	}
	defer func() { _ = sqliteDB.Close() }()
	// VACUUM INTO fills an empty file with a copy that is whole, whatever is written meanwhile.
	if _, err := sqliteDB.ExecContext(ctx, "VACUUM INTO ?", out); err != nil {
		_ = os.Remove(out)
		return "", fmt.Errorf("writing backup: %w", err)
	}
	return out, nil
}

// backupName is dated, so a backup never replaces an older one.
func backupName(path string) string {
	return path + "." + time.Now().UTC().Format("20060102-150405") + ".backup"
}

// KeepBackups copies inkan.db into dir once a day and keeps the last backupsKept, until ctx is done.
func KeepBackups(ctx context.Context, dataDir, dir string) {
	for {
		if err := dailyBackup(ctx, dataDir, dir, time.Now()); err != nil && ctx.Err() == nil {
			slog.Error("The daily backup failed", "dir", dir, "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Hour):
		}
	}
}

// dailyBackup writes the copy of the day unless it is there and drops the oldest beyond backupsKept.
func dailyBackup(ctx context.Context, dataDir, dir string, now time.Time) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating backup directory: %w", err)
	}
	out := filepath.Join(dir, "inkan-"+now.UTC().Format(time.DateOnly)+".db")
	if _, err := os.Stat(out); err == nil {
		return nil
	}
	// A copy cut short by a crash must not pass for the one of the day.
	tmp := out + ".tmp"
	_ = os.Remove(tmp)
	if _, err := Backup(ctx, dataDir, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, out); err != nil {
		return fmt.Errorf("finishing backup: %w", err)
	}

	copies, err := fs.Glob(os.DirFS(dir), "inkan-[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9].db")
	if err != nil {
		return err
	}
	slices.Sort(copies)
	for _, old := range copies[:max(0, len(copies)-backupsKept)] {
		if err := os.Remove(filepath.Join(dir, old)); err != nil {
			return fmt.Errorf("removing an old backup: %w", err)
		}
	}
	return nil
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
