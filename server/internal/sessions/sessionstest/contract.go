package sessionstest

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/aduverger/prunelle/server/internal/sessions"
)

// Subject is a backend under contract test, prepared with at least one runtime.
type Subject struct {
	Backend sessions.Backend
	// MissingID is an external ID the backend does not know.
	MissingID string
	// Change makes the backend emit an event about an existing runtime.
	Change func()
}

// RunContract checks the behavior the sessions service relies on.
func RunContract(t *testing.T, newSubject func(t *testing.T) Subject) {
	t.Run("reports kind and capabilities", func(t *testing.T) {
		s := newSubject(t)
		if s.Backend.Kind() == "" {
			t.Error("empty kind")
		}
		caps := s.Backend.Capabilities()
		for _, c := range []string{sessions.CapabilityDiscover, sessions.CapabilityInspect, sessions.CapabilitySubscribe} {
			if !slices.Contains(caps, c) {
				t.Errorf("capabilities %v miss %s", caps, c)
			}
		}
	})

	t.Run("discovers runtimes that inspect consistently", func(t *testing.T) {
		ctx := context.Background()
		s := newSubject(t)
		d, err := s.Backend.Discover(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(d.Runtimes) == 0 {
			t.Fatal("no runtimes discovered")
		}
		seen := map[string]bool{}
		for _, r := range d.Runtimes {
			if r.ExternalID == "" || seen[r.ExternalID] {
				t.Errorf("missing or duplicate external ID %q", r.ExternalID)
			}
			seen[r.ExternalID] = true
			if !slices.Contains([]string{sessions.StateOnline, sessions.StateOffline, sessions.StateStopped, sessions.StateUnknown}, r.State) {
				t.Errorf("%s: invalid state %q", r.ExternalID, r.State)
			}
			if r.Label == "" {
				t.Errorf("%s: empty label", r.ExternalID)
			}
			if len(r.Metadata) > 0 && !json.Valid(r.Metadata) {
				t.Errorf("%s: metadata is not JSON", r.ExternalID)
			}
			inspected, err := s.Backend.Inspect(ctx, r.ExternalID)
			if err != nil {
				t.Fatalf("inspect %s: %v", r.ExternalID, err)
			}
			if inspected.ExternalID != r.ExternalID || inspected.Label != r.Label || inspected.State != r.State {
				t.Errorf("inspect %s = %+v, discovered %+v", r.ExternalID, inspected, r)
			}
		}
	})

	t.Run("reports unknown runtimes as not found", func(t *testing.T) {
		s := newSubject(t)
		_, err := s.Backend.Inspect(context.Background(), s.MissingID)
		if !errors.Is(err, sessions.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("streams changes and closes on cancel", func(t *testing.T) {
		s := newSubject(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		events, err := s.Backend.Subscribe(ctx)
		if err != nil {
			t.Fatal(err)
		}
		s.Change()
		select {
		case <-events:
		case <-time.After(5 * time.Second):
			t.Fatal("no event after change")
		}
		cancel()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case _, open := <-events:
				if !open {
					return
				}
			case <-deadline:
				t.Fatal("stream not closed after cancel")
			}
		}
	})
}
