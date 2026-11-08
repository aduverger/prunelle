package rex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/aduverger/prunelle/server/internal/sessions"
	"github.com/aduverger/prunelle/server/internal/sessions/sessionstest"
)

// Responses recorded from Rex API revision 10, with identifiers and paths shortened.
var recorded = map[string]string{
	"server.status":  `{"pid":1435,"instance_id":"inst-1","host_id":"host-local","started_at":"2026-11-06T09:29:09Z","exit_empty":true,"session_count":2,"tailscale":{"state":"disabled","allow_tailnet_members":false},"api_revision":10,"config":{"generation":1,"hash":""}}`,
	"server.version": `{"rex_version":"0.0.1793741575-g1b6654","platform":"linux_arm64","commit":"1b66548","built_at":"2026-11-03T21:32:55Z","dirty":false,"api_revision":10}`,
	"session.list":   `{"sessions":[{"session_id":"session:a","label":"prunelle"},{"session_id":"session:b","label":"notes"}]}`,
	"session:a session.list_blocks": `{"blocks":[
		{"block_id":"block:a1","label":"shell","creator_name":"com.superlogical.terminal","flavor":"com.superlogical.terminal.shell","host_id":"host-local","placement":"placed","window_id":"window:w1","owner":"client:c1"},
		{"block_id":"block:a2","label":"tour","creator_name":"com.superlogical.onboarding","flavor":"com.superlogical.onboarding","host_id":"host-local","placement":"placed"}]}`,
	"session:b session.list_blocks": `{"blocks":[
		{"block_id":"block:b1","label":"shell","creator_name":"com.superlogical.terminal","flavor":"com.superlogical.terminal.shell","host_id":"host-remote","placement":"detached"}]}`,
	"session:a block:a1 com.superlogical.terminal.process": `{"last_exit":null,
		"child":{"pid":1473,"user_id":1000,"user":"dev","name":"zsh","path":"/usr/bin/zsh","invoked_path":"/usr/bin/zsh","argv0":"zsh","cwd":"/work/api"},
		"foreground":{"pid":253229,"user_id":1000,"user":"dev","name":"node","path":"/opt/node/bin/node","argv0":"pi","cwd":"/work/api/src"},
		"ancestors":[{"pid":1473,"name":"zsh","argv0":"zsh","cwd":"/work/api"}],"home":"/home/dev"}`,
	"session:a block:a1 com.superlogical.terminal.pwd":            `{"pwd":"file://devbox/work/api","owner":{"pid":1473,"name":"zsh"}}`,
	"session:a block:a1 com.superlogical.terminal.program_status": `{"records":[{"state":"working","app":"pi","effective_app":"pi","updated_at":"2026-11-08T13:57:44Z","owner":{"pid":253229,"name":"node"}}]}`,
	"session:b block:b1 com.superlogical.terminal.process":        `{"last_exit":{"exit_code":0},"child":{"pid":2001,"name":"zsh","argv0":"zsh"},"foreground":null,"ancestors":null,"home":"/home/dev"}`,
	"session:b block:b1 com.superlogical.terminal.pwd":            `{"pwd":"file://other/home/dev/notes"}`,
	"session:b block:b1 com.superlogical.terminal.program_status": `{"records":[]}`,
}

// replay answers `rex api call` invocations from recorded, keyed by session, block and method.
func replay(responses map[string]string, calls *[]string) Runner {
	return func(_ context.Context, args ...string) ([]byte, error) {
		*calls = append(*calls, strings.Join(args, " "))
		if args[0] != "--autostart=false" {
			return nil, fmt.Errorf("autostart not disabled: %v", args)
		}
		i := slices.Index(args, "call")
		if i < 0 {
			return nil, fmt.Errorf("unexpected invocation %v", args)
		}
		rest := args[i+1:]
		var key []string
		if rest[0] == "-s" {
			key, rest = append(key, rest[1]), rest[2:]
		}
		if len(rest) > 1 {
			var params struct {
				BlockID string `json:"block_id"`
			}
			if err := json.Unmarshal([]byte(rest[1]), &params); err != nil {
				return nil, err
			}
			key = append(key, params.BlockID)
		}
		key = append(key, rest[0])
		if out, ok := responses[strings.Join(key, " ")]; ok {
			return []byte(out), nil
		}
		return nil, &CLIError{ExitCode: exitNotFound, Message: "target not found (status=404 code=not_found)"}
	}
}

// pipeStreamer serves `rex events` from a pipe the test writes to.
type pipeStreamer struct {
	w *io.PipeWriter
}

func (p *pipeStreamer) stream(_ context.Context, args ...string) (io.ReadCloser, error) {
	if !slices.Equal(args, []string{"--autostart=false", "events", "--all", "--json"}) {
		return nil, fmt.Errorf("unexpected events invocation %v", args)
	}
	r, w := io.Pipe()
	p.w = w
	return r, nil
}

func newReplayBackend(t *testing.T) (*Backend, *pipeStreamer, *[]string) {
	t.Helper()
	calls := &[]string{}
	streamer := &pipeStreamer{}
	return NewWithCLI(Config{}, replay(recorded, calls), streamer.stream), streamer, calls
}

func TestContract(t *testing.T) {
	sessionstest.RunContract(t, func(t *testing.T) sessionstest.Subject {
		b, streamer, _ := newReplayBackend(t)
		return sessionstest.Subject{
			Backend:   b,
			MissingID: "session:a/block:gone",
			Change: func() {
				fmt.Fprintln(streamer.w, `{"type":"session_event","payload":{"session_id":"session:a","event":{"type":"block_event","payload":{"block_id":"block:a1","creator_name":"com.superlogical.terminal","name":"pwd_changed","payload":{"pwd":"file://devbox/work"}}}}}`)
			},
		}
	})
}

func TestDiscoverDescribesTerminalBlocks(t *testing.T) {
	b, _, _ := newReplayBackend(t)
	d, err := b.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if d.Info["api_revision"] != "10" || d.Info["rex_version"] == "" {
		t.Errorf("info = %v", d.Info)
	}
	if len(d.Runtimes) != 2 {
		t.Fatalf("runtimes = %+v", d.Runtimes)
	}
	pi, notes := d.Runtimes[0], d.Runtimes[1]
	if pi.ExternalID != "session:a/block:a1" || pi.State != sessions.StateOnline || pi.Label != "prunelle / shell" ||
		pi.Cwd != "/work/api/src" || pi.Process != "pi" || !pi.Local || pi.HarnessHint != "pi" {
		t.Errorf("pi runtime = %+v", pi)
	}
	var meta metadata
	if err := json.Unmarshal(pi.Metadata, &meta); err != nil || meta.BlockID != "block:a1" || meta.ProgramStatus[0].State != "working" {
		t.Errorf("metadata = %s", pi.Metadata)
	}
	if notes.State != sessions.StateStopped || notes.Local || notes.Cwd != "/home/dev/notes" || notes.HarnessHint != "" || notes.Process != "zsh" {
		t.Errorf("exited remote runtime = %+v", notes)
	}
}

func TestCLIFailures(t *testing.T) {
	failing := func(err error) Runner {
		return func(context.Context, ...string) ([]byte, error) { return nil, err }
	}
	notRunning := &CLIError{ExitCode: 4, Message: "no Rex server is running at /run/rex.sock, and autostart is disabled"}
	b := NewWithCLI(Config{}, failing(notRunning), nil)
	if _, err := b.Discover(context.Background()); !errors.As(err, new(*CLIError)) || errors.Is(err, sessions.ErrNotFound) {
		t.Errorf("unreachable server: %v", err)
	}
	b = NewWithCLI(Config{}, failing(&CLIError{ExitCode: exitNotFound, Message: "session target not found"}), nil)
	if _, err := b.Inspect(context.Background(), "session:x/block:y"); !errors.Is(err, sessions.ErrNotFound) {
		t.Errorf("missing target: %v", err)
	}
	if _, err := b.Inspect(context.Background(), "not-a-rex-id"); !errors.Is(err, sessions.ErrNotFound) {
		t.Errorf("malformed id: %v", err)
	}
}

// A configured server must reach every call; otherwise Prunelle silently observes the local Rex instead.
func TestEveryCallTargetsConfiguredServer(t *testing.T) {
	calls := &[]string{}
	b := NewWithCLI(Config{Server: "https://rex.example.ts.net"}, replay(recorded, calls), nil)
	if _, err := b.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, call := range *calls {
		if !strings.Contains(call, " -S https://rex.example.ts.net ") {
			t.Errorf("call without the configured server: %s", call)
		}
	}
}

func TestParseEvent(t *testing.T) {
	cases := []struct {
		line string
		want *sessions.Event
	}{
		{`{"type":"session_created","payload":{"session":{"session_id":"session:c","label":"x"}}}`, &sessions.Event{}},
		{`{"type":"session_destroyed","payload":{"session_id":"session:c"}}`, &sessions.Event{}},
		{`{"type":"session_event","payload":{"session_id":"session:a","event":{"type":"block_closed","payload":{"block_id":"block:a1"}}}}`, &sessions.Event{ExternalID: "session:a/block:a1"}},
		{`{"type":"session_event","payload":{"session_id":"session:a","event":{"type":"block_event","payload":{"block_id":"block:a1","creator_name":"com.superlogical.terminal","name":"child_exited","payload":{"exit_code":2,"error":"exit status 2"}}}}}`, &sessions.Event{ExternalID: "session:a/block:a1"}},
		{`{"type":"session_event","payload":{"session_id":"session:a","event":{"type":"block_event","payload":{"block_id":"block:a1","creator_name":"com.superlogical.terminal","name":"bell","payload":{}}}}}`, nil},
		{`{"type":"session_event","payload":{"session_id":"session:a","event":{"type":"block_event","payload":{"block_id":"block:a2","creator_name":"com.superlogical.onboarding","name":"step_changed","payload":{}}}}}`, nil},
		{`{"type":"session_event","payload":{"session_id":"session:a","event":{"type":"session_view_changed","payload":{"revision":3}}}}`, nil},
		{`{"type":"client_joined","payload":{"client":{}}}`, nil},
		{`Error: interrupted`, nil},
	}
	for _, tc := range cases {
		got, ok := parseEvent([]byte(tc.line))
		if (tc.want == nil) == ok || (ok && got != *tc.want) {
			t.Errorf("parseEvent(%s) = %+v, %v; want %+v", tc.line, got, ok, tc.want)
		}
	}
}

// TestLiveRex discovers the local Rex server. Run with PRUNELLE_REX_LIVE=1.
func TestLiveRex(t *testing.T) {
	if os.Getenv("PRUNELLE_REX_LIVE") == "" {
		t.Skip("set PRUNELLE_REX_LIVE=1 to test against the local Rex server")
	}
	b := New(Config{})
	ctx := context.Background()
	d, err := b.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("rex %v", d.Info)
	for _, r := range d.Runtimes {
		t.Logf("%s state=%s label=%q cwd=%s process=%s local=%v harness=%q", r.ExternalID, r.State, r.Label, r.Cwd, r.Process, r.Local, r.HarnessHint)
		inspected, err := b.Inspect(ctx, r.ExternalID)
		if err != nil || inspected.ExternalID != r.ExternalID {
			t.Errorf("inspect %s: %+v, %v", r.ExternalID, inspected, err)
		}
	}
	if _, err := b.Inspect(ctx, "session:00000000-0000-0000-0000-000000000000/block:00000000-0000-0000-0000-000000000000"); !errors.Is(err, sessions.ErrNotFound) {
		t.Errorf("missing runtime: %v", err)
	}
}
