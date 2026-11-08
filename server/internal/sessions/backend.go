// Package sessions discovers runtime sessions (processes, terminals) hosted by external session
// backends such as Rex, and records their explicit association with workspaces. It knows nothing
// about agent conversations; see package agents.
package sessions

import (
	"context"
	"encoding/json"
	"errors"
)

// Runtime session states. A backend that cannot be reached leaves its sessions UNKNOWN:
// losing the connection does not mean the process stopped.
const (
	StateOnline  = "ONLINE"
	StateOffline = "OFFLINE"
	StateStopped = "STOPPED"
	StateUnknown = "UNKNOWN"
)

// Capabilities a backend may report. Operations a backend lacks are simply not listed.
const (
	CapabilityDiscover  = "DISCOVER"
	CapabilityInspect   = "INSPECT"
	CapabilitySubscribe = "SUBSCRIBE"
)

// ErrNotFound reports a runtime the backend no longer knows.
var ErrNotFound = errors.New("runtime session not found")

// Backend discovers and observes runtime sessions of one provider.
type Backend interface {
	Kind() string
	Capabilities() []string
	// Discover returns every runtime the backend currently hosts.
	Discover(ctx context.Context) (Discovery, error)
	// Inspect returns one runtime, or ErrNotFound when it is gone.
	Inspect(ctx context.Context, externalID string) (Observed, error)
	// Subscribe streams change notifications until ctx is done or the stream breaks;
	// the channel is then closed. Callers recover lost events with a fresh Discover.
	Subscribe(ctx context.Context) (<-chan Event, error)
}

// Discovery is a full snapshot of a backend.
type Discovery struct {
	Runtimes []Observed
	// Info describes the backend endpoint, e.g. its version.
	Info map[string]string
}

// Observed is a provider-neutral description of one runtime session.
type Observed struct {
	ExternalID string
	State      string
	Label      string
	Cwd        string
	Process    string
	// Local reports whether the runtime runs on the same host as the backend, so Cwd is a path
	// on that machine.
	Local bool
	// HarnessHint names the coding harness the runtime appears to run, such as "pi". It is a
	// hint for later agent association, not an identity.
	HarnessHint string
	// Metadata holds backend-specific details.
	Metadata json.RawMessage
}

// Event tells the observer what to re-read. An empty ExternalID asks for a full rediscovery.
type Event struct {
	ExternalID string
}
