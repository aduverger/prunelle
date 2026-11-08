package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/aduverger/prunelle/server/api"
	"github.com/aduverger/prunelle/server/internal/events"
	"github.com/aduverger/prunelle/server/internal/review"
	"github.com/aduverger/prunelle/server/internal/store"
)

const (
	keepaliveInterval = 20 * time.Second
	eventBatchSize    = 500
)

func (h *handlers) StreamEvents(ctx context.Context, req api.StreamEventsRequestObject) (api.StreamEventsResponseObject, error) {
	after, err := resumePoint(ctx, h.db, req.Params)
	if err != nil {
		return nil, err
	}
	return eventStream{ctx: ctx, db: h.db, after: after}, nil
}

// resumePoint is the sequence after which the stream starts: Last-Event-ID (sent by reconnecting
// EventSource clients), else the after parameter, else the latest event.
func resumePoint(ctx context.Context, db *store.DB, params api.StreamEventsParams) (int64, error) {
	if params.LastEventID != nil {
		after, err := strconv.ParseInt(*params.LastEventID, 10, 64)
		if err != nil || after < 0 {
			return 0, &review.Error{Code: review.CodeInvalidArgument, Message: fmt.Sprintf("Last-Event-ID %q is not an event sequence", *params.LastEventID)}
		}
		return after, nil
	}
	if params.After != nil {
		return *params.After, nil
	}
	return events.Head(ctx, db)
}

// eventStream writes events as server-sent events until the client leaves or the server stops.
// The database is the source: a commit only wakes the stream, which then reads what it has not sent,
// so a slow or reconnecting client misses nothing.
type eventStream struct {
	ctx   context.Context
	db    *store.DB
	after int64
}

func (s eventStream) VisitStreamEventsResponse(w http.ResponseWriter) error {
	flusher := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	writeSSE(w, "ready", s.after, map[string]int64{"sequence": s.after})
	keepalive := time.NewTicker(keepaliveInterval)
	defer keepalive.Stop()
	after := s.after
	for {
		changed := s.db.Changed()
		batch, err := events.After(s.ctx, s.db, after, eventBatchSize)
		if err != nil {
			if s.ctx.Err() == nil {
				slog.Error("read events", "after", after, "err", err)
			}
			return nil
		}
		for _, e := range batch {
			writeSSE(w, e.Type, e.Sequence, e)
			after = e.Sequence
		}
		if err := flusher.Flush(); err != nil {
			return nil
		}
		if len(batch) == eventBatchSize {
			continue
		}
		select {
		case <-s.ctx.Done():
			return nil
		case <-changed:
		case <-keepalive.C:
			io.WriteString(w, ": keepalive\n\n")
		}
	}
}

func writeSSE(w io.Writer, event string, id int64, data any) {
	encoded, _ := json.Marshal(data) // events and maps of numbers always encode
	fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", id, event, encoded)
}
