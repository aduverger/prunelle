// Package sessionstest provides an in-memory session backend and the contract every backend must satisfy.
package sessionstest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/aduverger/prunelle/server/internal/sessions"
)

// Fake is an in-memory sessions.Backend whose runtimes and availability tests control.
type Fake struct {
	mu          sync.Mutex
	runtimes    map[string]sessions.Observed
	unreachable error
	inspectErr  error
	subscribers []chan sessions.Event
}

func NewFake(runtimes ...sessions.Observed) *Fake {
	f := &Fake{runtimes: map[string]sessions.Observed{}}
	for _, r := range runtimes {
		f.runtimes[r.ExternalID] = r
	}
	return f
}

func (f *Fake) Kind() string { return "REX" }

func (f *Fake) Capabilities() []string {
	return []string{sessions.CapabilityDiscover, sessions.CapabilityInspect, sessions.CapabilitySubscribe}
}

func (f *Fake) Discover(context.Context) (sessions.Discovery, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.unreachable != nil {
		return sessions.Discovery{}, f.unreachable
	}
	d := sessions.Discovery{Info: map[string]string{"backend": "fake"}}
	for _, r := range f.runtimes {
		d.Runtimes = append(d.Runtimes, r)
	}
	slices.SortFunc(d.Runtimes, func(a, b sessions.Observed) int {
		if a.ExternalID < b.ExternalID {
			return -1
		}
		return 1
	})
	return d, nil
}

func (f *Fake) Inspect(_ context.Context, externalID string) (sessions.Observed, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.unreachable != nil {
		return sessions.Observed{}, f.unreachable
	}
	if err := f.inspectErr; err != nil {
		f.inspectErr = nil
		return sessions.Observed{}, err
	}
	r, ok := f.runtimes[externalID]
	if !ok {
		return sessions.Observed{}, fmt.Errorf("%w: %s", sessions.ErrNotFound, externalID)
	}
	return r, nil
}

func (f *Fake) Subscribe(ctx context.Context) (<-chan sessions.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.unreachable != nil {
		return nil, f.unreachable
	}
	events := make(chan sessions.Event, 16)
	f.subscribers = append(f.subscribers, events)
	go func() {
		<-ctx.Done()
		f.mu.Lock()
		defer f.mu.Unlock()
		if i := slices.Index(f.subscribers, events); i >= 0 {
			f.subscribers = slices.Delete(f.subscribers, i, i+1)
			close(events)
		}
	}()
	return events, nil
}

// Set adds or replaces a runtime and notifies subscribers.
func (f *Fake) Set(r sessions.Observed) {
	f.mu.Lock()
	f.runtimes[r.ExternalID] = r
	f.mu.Unlock()
	f.notify(sessions.Event{ExternalID: r.ExternalID})
}

// Remove deletes a runtime and notifies subscribers.
func (f *Fake) Remove(externalID string) {
	f.mu.Lock()
	delete(f.runtimes, externalID)
	f.mu.Unlock()
	f.notify(sessions.Event{ExternalID: externalID})
}

// SetUnreachable makes every call fail with err (nil restores the backend) and ends open streams.
func (f *Fake) SetUnreachable(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unreachable = err
	if err != nil {
		for _, s := range f.subscribers {
			close(s)
		}
		f.subscribers = nil
	}
}

func (f *Fake) notify(e sessions.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.subscribers {
		s <- e
	}
}

// FailNextInspect makes the next Inspect call return err.
func (f *Fake) FailNextInspect(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inspectErr = err
}

// Subscriptions returns the number of open event streams.
func (f *Fake) Subscriptions() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.subscribers)
}

// ErrUnreachable is a convenient failure for SetUnreachable.
var ErrUnreachable = errors.New("fake backend unreachable")
