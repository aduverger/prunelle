package store

import (
	"context"
	"errors"
)

// SQLite's referenced-table rebuild requires foreign_keys off before BEGIN. This runs only
// during Open, before serving requests; the resulting graph is checked before committing.
func (db *DB) migrateReviewOwnership(ctx context.Context, migration string) (err error) {
	conn, err := db.writer.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return err
	}
	defer func() {
		_, restoreErr := conn.ExecContext(context.Background(), "PRAGMA foreign_keys = ON")
		var enabled int
		if restoreErr == nil {
			restoreErr = conn.QueryRowContext(context.Background(), "PRAGMA foreign_keys").Scan(&enabled)
			if restoreErr == nil && enabled != 1 {
				restoreErr = errors.New("foreign key enforcement was not restored")
			}
		}
		err = errors.Join(err, restoreErr)
	}()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, migration); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	invalid := rows.Next()
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if invalid {
		return errors.New("review ownership migration failed foreign key validation")
	}
	if _, err = tx.ExecContext(ctx, "PRAGMA user_version = 5"); err != nil {
		return err
	}
	return tx.Commit()
}
