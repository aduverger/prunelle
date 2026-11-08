// Package review owns the review domain: workspaces, reviews, revisions, manifest import,
// and the read model served by the API.
package review

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/aduverger/prunelle/server/internal/store"
)

// MaxRevisionContentBytes bounds the file contents stored for one revision.
const MaxRevisionContentBytes = 50 << 20

type Service struct {
	db           *store.DB
	allowedRoots []string
	now          func() time.Time
}

func NewService(db *store.DB, allowedRoots []string) *Service {
	return &Service{db: db, allowedRoots: allowedRoots, now: func() time.Time { return time.Now().UTC() }}
}

// Error codes map to HTTP statuses in the API layer.
const (
	CodeNotFound        = "NOT_FOUND"
	CodeInvalidArgument = "INVALID_ARGUMENT"
	CodeConflict        = "CONFLICT"
	CodeCaptureFailed   = "CAPTURE_FAILED"
)

type Error struct {
	Code    string
	Message string
	Details []string
}

func (e *Error) Error() string {
	if len(e.Details) == 0 {
		return e.Message
	}
	return e.Message + ": " + strings.Join(e.Details, "; ")
}

func notFound(kind, id string) error {
	return &Error{Code: CodeNotFound, Message: fmt.Sprintf("%s %s not found", kind, id)}
}

func invalid(format string, args ...any) error {
	return &Error{Code: CodeInvalidArgument, Message: fmt.Sprintf(format, args...)}
}

func newID(prefix string) string {
	return prefix + "_" + strings.ReplaceAll(uuid.Must(uuid.NewV7()).String(), "-", "")
}

const timeLayout = time.RFC3339Nano

func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

func parseTime(s string) time.Time {
	t, err := time.Parse(timeLayout, s)
	if err != nil {
		panic(fmt.Sprintf("corrupt timestamp %q: %v", s, err))
	}
	return t
}

// queryRow scans a single row, translating sql.ErrNoRows into a not-found error.
func scanOne(row *sql.Row, kind, id string, dest ...any) error {
	err := row.Scan(dest...)
	if errors.Is(err, sql.ErrNoRows) {
		return notFound(kind, id)
	}
	return err
}

func (s *Service) reader() *sql.DB { return s.db.Reader() }

func (s *Service) write(ctx context.Context, fn func(tx *sql.Tx) error) error {
	return s.db.Write(ctx, fn)
}

// queryStrings returns the first column of every row.
func queryStrings(ctx context.Context, q querier, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []string{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		values = append(values, v)
	}
	return values, rows.Err()
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func nullString(p *string) sql.NullString {
	if p == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *p, Valid: true}
}

func stringPtr(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	return &n.String
}
