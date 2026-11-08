// Package review owns the review domain: workspaces, reviews, revisions, manifest import,
// and the read model served by the API.
package review

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/aduverger/prunelle/server/internal/events"
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
	// CodePreconditionFailed reports an update based on a stale version of the resource.
	CodePreconditionFailed = "PRECONDITION_FAILED"
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

// problems collects validation failures, each prefixed with the path of the offending field.
type problems []string

func (p *problems) add(path, format string, args ...any) {
	*p = append(*p, path+": "+fmt.Sprintf(format, args...))
}

func (p *problems) oneOf(path, value string, allowed []string) {
	if !slices.Contains(allowed, value) {
		p.add(path, "%q is not one of %s", value, strings.Join(allowed, ", "))
	}
}

func (p *problems) required(path, value string) {
	if strings.TrimSpace(value) == "" {
		p.add(path, "is required")
	}
}

// err returns nil when nothing was reported.
func (p problems) err(message string) error {
	if len(p) == 0 {
		return nil
	}
	return &Error{Code: CodeInvalidArgument, Message: message, Details: p}
}

// checkVersion compares the stored version of a resource with the one the caller last read.
func checkVersion(kind, id string, stored, expected int) error {
	if stored != expected {
		return &Error{Code: CodePreconditionFailed, Message: fmt.Sprintf("%s %s is at version %d, not %d; reload it and retry", kind, id, stored, expected)}
	}
	return nil
}

// recordEvent appends the event announcing a change, in the change's transaction.
func recordEvent(ctx context.Context, tx *sql.Tx, typ, entityID, reviewID string, actor Author, at time.Time) error {
	var review *string
	if reviewID != "" {
		review = &reviewID
	}
	return events.Append(ctx, tx, typ, entityID, review, events.Actor(actor), at)
}

// scanOne scans a single row, translating sql.ErrNoRows into a not-found error.
func scanOne(row *sql.Row, kind, id string, dest ...any) error {
	err := row.Scan(dest...)
	if errors.Is(err, sql.ErrNoRows) {
		return notFound(kind, id)
	}
	return err
}

// queryStrings returns the first column of every row.
func queryStrings(ctx context.Context, db *sql.DB, query string, args ...any) ([]string, error) {
	rows, err := db.QueryContext(ctx, query, args...)
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
