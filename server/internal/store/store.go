// Package store owns the SQLite database: connections, migrations, and transactions.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// DB holds a single writer connection and a pool of reader connections.
// SQLite serializes writes; one writer avoids busy errors between our own writers.
type DB struct {
	writer *sql.DB
	reader *sql.DB
}

const fileName = "prunelle.db"

// Open opens (creating if needed) the database in dataDir and applies pending migrations.
func Open(ctx context.Context, dataDir string) (*DB, error) {
	path := filepath.Join(dataDir, fileName)
	writer, err := sql.Open("sqlite", dsn(path, "_pragma=journal_mode(WAL)", "_pragma=synchronous(NORMAL)", "_txlock=immediate"))
	if err != nil {
		return nil, err
	}
	writer.SetMaxOpenConns(1)
	reader, err := sql.Open("sqlite", dsn(path, "mode=ro"))
	if err != nil {
		writer.Close()
		return nil, err
	}
	db := &DB{writer: writer, reader: reader}
	if err := db.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func dsn(path string, params ...string) string {
	params = append([]string{"_pragma=busy_timeout(5000)", "_pragma=foreign_keys(1)"}, params...)
	u := url.URL{Scheme: "file", Path: path, RawQuery: strings.Join(params, "&")}
	return u.String()
}

func (db *DB) Close() error {
	return errors.Join(db.reader.Close(), db.writer.Close())
}

// Reader returns the read-only connection pool.
func (db *DB) Reader() *sql.DB { return db.reader }

// Write runs fn in a write transaction, committing if fn returns nil.
func (db *DB) Write(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := db.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (db *DB) migrate(ctx context.Context) error {
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}
	var version int
	if err := db.writer.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > len(migrations) {
		return fmt.Errorf("database schema version %d is newer than this server (%d)", version, len(migrations))
	}
	for i := version; i < len(migrations); i++ {
		err := db.Write(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
				return fmt.Errorf("migration %d: %w", i+1, err)
			}
			_, err := tx.ExecContext(ctx, "PRAGMA user_version = "+strconv.Itoa(i+1))
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func loadMigrations() ([]string, error) {
	names, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	migrations := make([]string, len(names))
	for i, name := range names {
		if want := fmt.Sprintf("migrations/%04d_", i+1); !strings.HasPrefix(name, want) {
			return nil, fmt.Errorf("migration %s is out of sequence, expected prefix %s", name, want)
		}
		content, err := migrationFiles.ReadFile(name)
		if err != nil {
			return nil, err
		}
		migrations[i] = string(content)
	}
	return migrations, nil
}
