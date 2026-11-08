// Package rex implements a sessions.Backend for Rex (superlogical.com/rex) through its CLI.
// Rex documents its API methods but not their wire protocol, so every call runs
// `rex api call` and events come from `rex events --json`. Verified against Rex API revision 10.
package rex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"strings"
	"sync"

	"github.com/aduverger/prunelle/server/internal/sessions"
)

const (
	Kind            = "REX"
	terminalCreator = "com.superlogical.terminal"
	callTimeout     = "5s"
	// exitNotFound is the CLI exit status when a session or block target does not exist.
	exitNotFound = 3
)

type Config struct {
	// Path is the rex CLI; empty means "rex" on PATH.
	Path string `json:"path,omitempty"`
	// Server is the -S value; empty means the default local server.
	Server string `json:"server,omitempty"`
}

// Runner executes the rex CLI with args and returns stdout.
type Runner func(ctx context.Context, args ...string) ([]byte, error)

// Streamer starts a long-running rex CLI command and returns its stdout. Closing it stops the command.
type Streamer func(ctx context.Context, args ...string) (io.ReadCloser, error)

type Backend struct {
	cfg    Config
	run    Runner
	stream Streamer
}

func New(cfg Config) *Backend {
	path := cfg.Path
	if path == "" {
		path = "rex"
	}
	return NewWithCLI(cfg, execRunner(path), execStreamer(path))
}

// NewWithCLI builds a backend over custom CLI runners; tests replay recorded Rex output.
func NewWithCLI(cfg Config, run Runner, stream Streamer) *Backend {
	return &Backend{cfg: cfg, run: run, stream: stream}
}

func (b *Backend) Kind() string { return Kind }

func (b *Backend) Capabilities() []string {
	return []string{sessions.CapabilityDiscover, sessions.CapabilityInspect, sessions.CapabilitySubscribe}
}

// CLIError is a failed rex invocation.
type CLIError struct {
	ExitCode int
	Message  string
}

func (e *CLIError) Error() string {
	return fmt.Sprintf("rex exited with status %d: %s", e.ExitCode, e.Message)
}

func (b *Backend) globalArgs() []string {
	args := []string{"--autostart=false"}
	if b.cfg.Server != "" {
		args = append(args, "-S", b.cfg.Server)
	}
	return args
}

// call runs `rex api call` and decodes the JSON result into out.
func (b *Backend) call(ctx context.Context, sessionID, method string, params any, out any) error {
	args := append(b.globalArgs(), "--timeout", callTimeout, "api", "call")
	if sessionID != "" {
		args = append(args, "-s", sessionID)
	}
	args = append(args, method)
	if params != nil {
		encoded, err := json.Marshal(params)
		if err != nil {
			return err
		}
		args = append(args, string(encoded))
	}
	stdout, err := b.run(ctx, args...)
	if err != nil {
		var cliErr *CLIError
		if errors.As(err, &cliErr) && cliErr.ExitCode == exitNotFound {
			return fmt.Errorf("%w: %s", sessions.ErrNotFound, cliErr.Message)
		}
		return err
	}
	if err := json.Unmarshal(stdout, out); err != nil {
		return fmt.Errorf("decode %s result: %w", method, err)
	}
	return nil
}

func (b *Backend) blockCall(ctx context.Context, sessionID, blockID, method string, out any) error {
	return b.call(ctx, sessionID, terminalCreator+"."+method, map[string]string{"block_id": blockID}, out)
}

type serverStatus struct {
	InstanceID  string `json:"instance_id"`
	HostID      string `json:"host_id"`
	APIRevision int    `json:"api_revision"`
}

type serverVersion struct {
	RexVersion string `json:"rex_version"`
}

type session struct {
	SessionID string `json:"session_id"`
	Label     string `json:"label"`
}

type block struct {
	BlockID     string `json:"block_id"`
	Label       string `json:"label"`
	CreatorName string `json:"creator_name"`
	Flavor      string `json:"flavor"`
	HostID      string `json:"host_id"`
	Placement   string `json:"placement"`
}

type processInfo struct {
	Name   string `json:"name"`
	Argv0  string `json:"argv0"`
	Path   string `json:"path,omitempty"`
	Script string `json:"script,omitempty"`
	Cwd    string `json:"cwd,omitempty"`
}

type processResult struct {
	LastExit   json.RawMessage `json:"last_exit"`
	Child      *processInfo    `json:"child"`
	Foreground *processInfo    `json:"foreground"`
}

type pwdResult struct {
	Pwd string `json:"pwd"`
}

type programStatus struct {
	App   string `json:"app,omitempty"`
	State string `json:"state,omitempty"`
	Msg   string `json:"msg,omitempty"`
}

// metadata is the Rex-specific extension stored with each runtime session.
type metadata struct {
	SessionID     string          `json:"rex_session_id"`
	SessionLabel  string          `json:"rex_session_label"`
	BlockID       string          `json:"rex_block_id"`
	BlockLabel    string          `json:"rex_block_label"`
	Flavor        string          `json:"flavor"`
	HostID        string          `json:"host_id"`
	Placement     string          `json:"placement"`
	Foreground    *processInfo    `json:"foreground"`
	ProgramStatus []programStatus `json:"program_status"`
	LastExit      json.RawMessage `json:"last_exit,omitempty"`
}

func externalID(sessionID, blockID string) string { return sessionID + "/" + blockID }

func splitExternalID(id string) (sessionID, blockID string, err error) {
	sessionID, blockID, ok := strings.Cut(id, "/")
	if !ok || !strings.HasPrefix(sessionID, "session:") || !strings.HasPrefix(blockID, "block:") {
		return "", "", fmt.Errorf("%w: malformed Rex runtime ID %q", sessions.ErrNotFound, id)
	}
	return sessionID, blockID, nil
}

func (b *Backend) Discover(ctx context.Context) (sessions.Discovery, error) {
	var status serverStatus
	if err := b.call(ctx, "", "server.status", nil, &status); err != nil {
		return sessions.Discovery{}, err
	}
	var version serverVersion
	if err := b.call(ctx, "", "server.version", nil, &version); err != nil {
		return sessions.Discovery{}, err
	}
	var list struct {
		Sessions []session `json:"sessions"`
	}
	if err := b.call(ctx, "", "session.list", nil, &list); err != nil {
		return sessions.Discovery{}, err
	}
	discovery := sessions.Discovery{Info: map[string]string{
		"rex_version": version.RexVersion, "api_revision": fmt.Sprint(status.APIRevision), "instance_id": status.InstanceID,
	}}
	for _, s := range list.Sessions {
		blocks, err := b.blocks(ctx, s.SessionID)
		if errors.Is(err, sessions.ErrNotFound) {
			continue // destroyed since session.list
		}
		if err != nil {
			return sessions.Discovery{}, err
		}
		for _, blk := range blocks {
			if blk.CreatorName != terminalCreator {
				continue
			}
			observed, err := b.observe(ctx, s, blk, status.HostID)
			if errors.Is(err, sessions.ErrNotFound) {
				continue // closed since list_blocks
			}
			if err != nil {
				return sessions.Discovery{}, err
			}
			discovery.Runtimes = append(discovery.Runtimes, observed)
		}
	}
	return discovery, nil
}

func (b *Backend) Inspect(ctx context.Context, id string) (sessions.Observed, error) {
	sessionID, blockID, err := splitExternalID(id)
	if err != nil {
		return sessions.Observed{}, err
	}
	var status serverStatus
	if err := b.call(ctx, "", "server.status", nil, &status); err != nil {
		return sessions.Observed{}, err
	}
	var list struct {
		Sessions []session `json:"sessions"`
	}
	if err := b.call(ctx, "", "session.list", nil, &list); err != nil {
		return sessions.Observed{}, err
	}
	for _, s := range list.Sessions {
		if s.SessionID != sessionID {
			continue
		}
		blocks, err := b.blocks(ctx, sessionID)
		if err != nil {
			return sessions.Observed{}, err
		}
		for _, blk := range blocks {
			if blk.BlockID == blockID && blk.CreatorName == terminalCreator {
				return b.observe(ctx, s, blk, status.HostID)
			}
		}
	}
	return sessions.Observed{}, fmt.Errorf("%w: %s", sessions.ErrNotFound, id)
}

func (b *Backend) blocks(ctx context.Context, sessionID string) ([]block, error) {
	var result struct {
		Blocks []block `json:"blocks"`
	}
	err := b.call(ctx, sessionID, "session.list_blocks", nil, &result)
	return result.Blocks, err
}

func (b *Backend) observe(ctx context.Context, s session, blk block, serverHostID string) (sessions.Observed, error) {
	var proc processResult
	if err := b.blockCall(ctx, s.SessionID, blk.BlockID, "process", &proc); err != nil {
		return sessions.Observed{}, err
	}
	var pwd pwdResult
	if err := b.blockCall(ctx, s.SessionID, blk.BlockID, "pwd", &pwd); err != nil {
		return sessions.Observed{}, err
	}
	var statuses struct {
		Records []programStatus `json:"records"`
	}
	if err := b.blockCall(ctx, s.SessionID, blk.BlockID, "program_status", &statuses); err != nil {
		return sessions.Observed{}, err
	}

	meta := metadata{
		SessionID: s.SessionID, SessionLabel: s.Label, BlockID: blk.BlockID, BlockLabel: blk.Label,
		Flavor: blk.Flavor, HostID: blk.HostID, Placement: blk.Placement, Foreground: proc.Foreground,
		ProgramStatus: append([]programStatus{}, statuses.Records...),
	}
	state := sessions.StateOnline
	if len(proc.LastExit) > 0 && string(proc.LastExit) != "null" {
		state = sessions.StateStopped
		meta.LastExit = proc.LastExit
	}
	encoded, err := json.Marshal(meta)
	if err != nil {
		return sessions.Observed{}, err
	}
	return sessions.Observed{
		ExternalID:  externalID(s.SessionID, blk.BlockID),
		State:       state,
		Label:       s.Label + " / " + blk.Label,
		Cwd:         cwd(proc, pwd.Pwd),
		Process:     processName(proc),
		Local:       blk.HostID != "" && blk.HostID == serverHostID,
		HarnessHint: harnessHint(proc.Foreground, statuses.Records),
		Metadata:    encoded,
	}, nil
}

func cwd(proc processResult, reported string) string {
	for _, p := range []*processInfo{proc.Foreground, proc.Child} {
		if p != nil && p.Cwd != "" {
			return p.Cwd
		}
	}
	if u, err := url.Parse(reported); err == nil && u.Scheme == "file" {
		return u.Path
	}
	return reported
}

func processName(proc processResult) string {
	for _, p := range []*processInfo{proc.Foreground, proc.Child} {
		if p == nil {
			continue
		}
		if p.Argv0 != "" {
			return p.Argv0
		}
		return p.Name
	}
	return ""
}

func harnessHint(fg *processInfo, statuses []programStatus) string {
	if fg != nil && fg.Argv0 == "pi" {
		return "pi"
	}
	for _, s := range statuses {
		if s.App == "pi" {
			return "pi"
		}
	}
	return ""
}

// Subscribe follows `rex events --all --json` and maps runtime-relevant events.
func (b *Backend) Subscribe(ctx context.Context) (<-chan sessions.Event, error) {
	stdout, err := b.stream(ctx, append(b.globalArgs(), "events", "--all", "--json")...)
	if err != nil {
		return nil, err
	}
	events := make(chan sessions.Event)
	done := make(chan struct{})
	go func() {
		// Closing the stream unblocks the reader when ctx ends while Rex is quiet.
		select {
		case <-ctx.Done():
		case <-done:
		}
		stdout.Close()
	}()
	go func() {
		defer close(events)
		defer close(done)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), 4<<20)
		for scanner.Scan() {
			event, ok := parseEvent(scanner.Bytes())
			if !ok {
				continue
			}
			select {
			case events <- event:
			case <-ctx.Done():
				return
			}
		}
	}()
	return events, nil
}

type envelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

var blockEventNames = map[string]bool{
	"child_exited": true, "process_changed": true, "pwd_changed": true,
	"program_status_changed": true, "program_status_removed": true,
}

func parseEvent(line []byte) (sessions.Event, bool) {
	var top envelope
	if json.Unmarshal(line, &top) != nil {
		return sessions.Event{}, false
	}
	switch top.Type {
	case "session_created", "session_destroyed":
		return sessions.Event{}, true
	case "session_event":
	default:
		return sessions.Event{}, false
	}
	var sessionEvent struct {
		SessionID string   `json:"session_id"`
		Event     envelope `json:"event"`
	}
	if json.Unmarshal(top.Payload, &sessionEvent) != nil {
		return sessions.Event{}, false
	}
	var target struct {
		BlockID     string `json:"block_id"`
		CreatorName string `json:"creator_name"`
		Name        string `json:"name"`
	}
	if json.Unmarshal(sessionEvent.Event.Payload, &target) != nil || target.BlockID == "" {
		return sessions.Event{}, false
	}
	switch sessionEvent.Event.Type {
	case "block_created", "block_closed":
	case "block_event":
		if target.CreatorName != terminalCreator || !blockEventNames[target.Name] {
			return sessions.Event{}, false
		}
	default:
		return sessions.Event{}, false
	}
	return sessions.Event{ExternalID: externalID(sessionEvent.SessionID, target.BlockID)}, true
}

func execRunner(path string) Runner {
	return func(ctx context.Context, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, path, args...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, &CLIError{ExitCode: exitErr.ExitCode(), Message: strings.TrimSpace(strings.TrimPrefix(stderr.String(), "Error: "))}
		}
		if err != nil {
			return nil, fmt.Errorf("run rex: %w", err)
		}
		return stdout.Bytes(), nil
	}
}

func execStreamer(path string) Streamer {
	return func(ctx context.Context, args ...string) (io.ReadCloser, error) {
		ctx, cancel := context.WithCancel(ctx)
		cmd := exec.CommandContext(ctx, path, args...)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			cancel()
			return nil, err
		}
		if err := cmd.Start(); err != nil {
			cancel()
			return nil, fmt.Errorf("run rex: %w", err)
		}
		return &commandStream{ReadCloser: stdout, cmd: cmd, cancel: cancel}, nil
	}
}

type commandStream struct {
	io.ReadCloser
	cmd    *exec.Cmd
	cancel context.CancelFunc
	once   sync.Once
}

func (s *commandStream) Close() error {
	s.once.Do(func() {
		s.cancel()
		s.ReadCloser.Close()
		s.cmd.Wait()
	})
	return nil
}
