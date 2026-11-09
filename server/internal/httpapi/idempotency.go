package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/aduverger/prunelle/server/internal/review"
	"github.com/aduverger/prunelle/server/internal/store"
)

// Idempotency-Key handling for POST requests. A key is claimed in the database before the request
// runs, so a concurrent duplicate or a retry after a crash cannot run the request twice:
//   - 2xx: the response is stored and replayed for the same key and request;
//   - 4xx: the request changed nothing, the key is released and can be retried;
//   - 5xx, or a crash while running: the outcome is unknown, so the key stays claimed and reports a
//     conflict until it expires; the client checks the state and retries with a new key.

const (
	idempotencyHeader = "Idempotency-Key"
	replayedHeader    = "Idempotent-Replayed"
	idempotencyTTL    = 24 * time.Hour
)

// replayedHeaders are the response headers stored with a response.
var replayedHeaders = []string{"Content-Type", "ETag"}

type idempotency struct {
	db *store.DB
}

type storedResponse struct {
	status  int
	headers map[string]string
	body    []byte
}

func (i *idempotency) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get(idempotencyHeader)
		if r.Method != http.MethodPost || key == "" {
			next.ServeHTTP(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, review.CodeInvalidArgument, "cannot read request body", nil)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		actor := callerFrom(r.Context())
		caller := actor.Login + " " + actor.ID
		hash := sha256.Sum256([]byte(r.Method + "\n" + r.URL.RequestURI() + "\n" + string(body)))

		stored, err := i.claim(r.Context(), caller, key, hex.EncodeToString(hash[:]))
		if err != nil {
			writeResponseError(w, r, err)
			return
		}
		if stored != nil {
			replay(w, *stored)
			return
		}
		recorder := &recordingWriter{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		switch status := recorder.statusCode(); {
		case status < 300:
			err = i.complete(caller, key, storedResponse{status: status, headers: recorder.storedHeaders(), body: recorder.body.Bytes()})
		case status < 500:
			err = i.release(caller, key)
		}
		if err != nil {
			slog.Error("record idempotency key", "key", key, "err", err)
		}
	})
}

// claim registers the key for this request, or returns the response stored for it. A key that is
// still running, interrupted, or used for another request is a conflict.
func (i *idempotency) claim(ctx context.Context, caller, key, requestHash string) (*storedResponse, error) {
	var stored *storedResponse
	err := i.db.Write(ctx, func(tx *sql.Tx) error {
		now := time.Now()
		if _, err := tx.ExecContext(ctx, `DELETE FROM idempotency_keys WHERE created_at < ?`, store.FormatTime(now.Add(-idempotencyTTL))); err != nil {
			return err
		}
		var hash string
		var status sql.NullInt64
		var headers sql.NullString
		var body []byte
		err := tx.QueryRowContext(ctx, `SELECT request_hash, status, headers, body FROM idempotency_keys WHERE caller = ? AND key = ?`,
			caller, key).Scan(&hash, &status, &headers, &body)
		if errors.Is(err, sql.ErrNoRows) {
			_, err := tx.ExecContext(ctx, `INSERT INTO idempotency_keys (caller, key, request_hash, created_at) VALUES (?, ?, ?, ?)`,
				caller, key, requestHash, store.FormatTime(now))
			return err
		}
		if err != nil {
			return err
		}
		switch {
		case hash != requestHash:
			return &review.Error{Code: review.CodeConflict, Message: "this Idempotency-Key was used for a different request"}
		case !status.Valid:
			return &review.Error{Code: review.CodeConflict,
				Message: "a request with this Idempotency-Key is running or was interrupted; check the current state, then retry with a new key"}
		}
		stored = &storedResponse{status: int(status.Int64), headers: map[string]string{}, body: body}
		return json.Unmarshal([]byte(headers.String), &stored.headers)
	})
	return stored, err
}

// complete and release run after the request, even if the client is gone.
func (i *idempotency) complete(caller, key string, response storedResponse) error {
	var resource struct {
		ID       string `json:"id"`
		ImportID string `json:"import_id"`
	}
	if len(response.body) != 0 {
		if err := json.Unmarshal(response.body, &resource); err != nil {
			return err
		}
	}
	id := resource.ID
	if id == "" {
		id = resource.ImportID
	}
	headers, err := json.Marshal(response.headers)
	if err != nil {
		return err
	}
	return i.db.Write(context.Background(), func(tx *sql.Tx) error {
		// Deletion can commit after the mutation but before its receipt: do not cache deleted content.
		_, err := tx.ExecContext(context.Background(), `UPDATE idempotency_keys SET status = ?, headers = ?, body = ?,
			review_id = (SELECT review_id FROM resource_reviews WHERE id = ?)
			WHERE caller = ? AND key = ? AND (? = ''
				OR EXISTS (SELECT 1 FROM resource_reviews WHERE id = ?)
				OR EXISTS (SELECT 1 FROM runtime_sessions WHERE id = ?))`,
			response.status, string(headers), response.body, id, caller, key, id, id, id)
		return err
	})
}

func (i *idempotency) release(caller, key string) error {
	return i.db.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `DELETE FROM idempotency_keys WHERE caller = ? AND key = ?`, caller, key)
		return err
	})
}

func replay(w http.ResponseWriter, response storedResponse) {
	for name, value := range response.headers {
		w.Header().Set(name, value)
	}
	w.Header().Set(replayedHeader, "true")
	w.WriteHeader(response.status)
	w.Write(response.body)
}

// recordingWriter passes a response through while keeping a copy.
type recordingWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (w *recordingWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *recordingWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

func (w *recordingWriter) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (w *recordingWriter) storedHeaders() map[string]string {
	headers := map[string]string{}
	for _, name := range replayedHeaders {
		if value := w.Header().Get(name); value != "" {
			headers[name] = value
		}
	}
	return headers
}
