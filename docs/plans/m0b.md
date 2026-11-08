# M0b - Rex session backend spike

Goal: with Rex configured, Prunelle discovers existing Rex terminal blocks, keeps their status current from Rex events, and lets a user associate one explicitly with a workspace. With Rex disabled, missing, or unreachable, everything in M0a keeps working and the session API reports the integration as unavailable. API and CLI only; no client UI.

Stacked on M0a (`m0a-foundation`): it extends M0a's schema, API, and CLI.

## Verified Rex facts (Rex 0.0.1793741575, server API revision 10)

- `session.list` → sessions; `session.list_blocks` (needs `session_id`) → blocks with `block_id`, `label`, `creator_name`, `flavor`, `host_id`, `placement`.
- Terminal block methods via `rex api call -s SESSION com.superlogical.terminal.<m> '{"block_id": ...}'`: `process` (foreground process: `name`, `argv0`, `path`, `cwd`; `last_exit`), `pwd` (`file://host/path` URL), `program_status` (records with `app`, `state`, `msg`; Pi reports `app: "pi"`).
- `server.status` → `instance_id`, `host_id`, `api_revision`; `server.version` → `rex_version`.
- `rex events --all --json` streams NDJSON: `session_created`/`session_destroyed` at top level; session events wrapped in `session_event` (`block_created`, `block_closed`), terminal events wrapped again in `block_event` (`child_exited`, `process_changed`, `pwd_changed`, `program_status_changed`, `program_status_removed`). The server drops a connection that falls behind instead of skipping events.
- Block IDs survive a Rex server restart; `instance_id` changes.
- The CLI autostarts a local server by default; Prunelle always passes `--autostart=false`.

## Design

### Interfaces (provider-neutral)

`internal/sessions`:

```go
type Backend interface {
    Kind() string
    Capabilities() []string
    Discover(ctx) (Discovery, error)            // full snapshot of runtimes
    Inspect(ctx, externalID string) (Observed, error)   // ErrNotFound when gone
    Subscribe(ctx) (<-chan Event, error)        // closed when the stream ends or ctx is done
}
```

`Observed` is a provider-neutral descriptor: external ID, state, label, cwd, foreground process, local-host flag, harness hint, backend metadata (JSON). `Event` names an external ID (or "all") whose state must be re-read.

`internal/agents` declares `AgentAdapter` (history, send, subscribe) as a separate interface with no implementation; it arrives with Pi in M4.

### Rex backend

- One Rex block = one runtime session. External ID `session:<uuid>/block:<uuid>` (block methods need the session).
- Every call: `rex --autostart=false [-S server] --timeout 5s api call ...` via `exec` without a shell. The CLI path and server address come from config.
- Discover: `server.status`, `session.list`, then per session `session.list_blocks`, then per terminal block `process`, `pwd`, `program_status`.
- State: `ONLINE` when the block exists and its program runs; `STOPPED` when the block closed or its program exited; `UNKNOWN` when Rex is unreachable (last observation kept). `OFFLINE` stays in the enum for other backends.
- Cwd: foreground process `cwd`, else `pwd`. Only blocks whose `host_id` equals the Rex server's host are marked local.
- Harness hint `pi` when the foreground `argv0` is `pi` or a program status has `app` `pi`. A hint only: no agent session is created.

### Observer

Per enabled backend, a goroutine: discover → reconcile → subscribe; on each event re-inspect the affected runtime (or rediscover on session-level events); when the stream ends, back off (1 s doubling to 30 s) and restart with a fresh discovery. Errors mark the backend unreachable and its runtimes `UNKNOWN`; they never affect review routes.

### Persistence (migration 2)

- `session_backends` (id, name unique, kind `REX`/`HERDR`/`MANAGED`, config JSON, enabled). Synced from the config file at startup; backends removed from the config are disabled, not deleted.
- `runtime_sessions` (Prunelle ID, backend, external ID unique per backend, state, label, cwd, process, local, harness hint, metadata JSON, capabilities JSON, first/last seen, state changed at).
- `workspace_runtime_sessions` (workspace, runtime session): explicit associations.

Backend health (reachable, error, Rex version/API revision, checked at) is in memory.

### Configuration

```toml
[[session_backends]]
name = "rex"
kind = "REX"
# rex_path = "rex"     # CLI path
# server = ""          # rex -S value; empty = default local server
```

Backends are configured in the file, not through the API, so remote users cannot point the server at arbitrary Rex servers.

### API

```text
GET    /v1/session-backends
GET    /v1/runtime-sessions?workspace_id=      associated sessions when filtered
GET    /v1/runtime-sessions/{id}
PUT    /v1/workspaces/{id}/runtime-sessions/{runtimeSessionId}   associate (idempotent)
DELETE /v1/workspaces/{id}/runtime-sessions/{runtimeSessionId}   remove association
```

Each runtime session lists `workspace_ids` (explicit) and `suggested_workspace_ids` (local cwd inside a workspace repository root, computed on read, never auto-applied).

### CLI

`prunelle sessions backends | ls [--workspace ID] | link RUNTIME_ID WORKSPACE_ID | unlink RUNTIME_ID WORKSPACE_ID`.

## Tests

- Contract suite (`sessionstest`) run against the fake backend and the Rex backend driven by recorded Rex CLI output.
- Rex parsing: discovery from recorded responses; events from recorded NDJSON; unreachable Rex → error.
- Service: reconcile assigns stable Prunelle IDs, closed blocks become `STOPPED`, backend failure makes runtimes `UNKNOWN`, association requires existing records, suggestions from cwd.
- API: server with an unreachable Rex backend still serves the full fixture review; session routes report unavailability; association round trip with the fake backend.
- Opt-in live test (`PRUNELLE_REX_LIVE=1`) against the local Rex server.

## Out of scope

Pi adapter, agent sessions, review-agent bindings, terminal attach, start/stop, Herdr, managed sessions, client UI.

## Implementation notes

- Discovery against the local Rex (4 sessions) takes about 1 s: roughly 20 CLI invocations of ~50 ms each. Acceptable for event-driven refresh; a native client would remove the per-call process cost if it ever matters.
- Rex CLI exit status 3 means "target not found" and 4 means "no server running" (verified on this version); status 3 maps to `ErrNotFound`.
- `store` now owns the ID and timestamp helpers shared by `review` and `sessions`.

## Decision ledger

Choices made where the spec and plan were silent, least confident first.

1. **Only terminal blocks are runtimes.** Blocks from other creators (e.g. Rex's onboarding block) are ignored. *Would have asked:* should non-terminal blocks appear for completeness?
2. **`STOPPED` covers both "block closed" and "program exited but block kept open".** Rex keeps the block when started with `--keep-open`; the program inside has exited, so the runtime is not running. *Would have asked:* do you want a distinct state for a lingering closed-program block?
3. **Harness hint from `argv0 == "pi"` or a `program_status` record with `app == "pi"`.** Pi launched through a wrapper script with another `argv0` and no program status would be missed. It is only a hint; nothing depends on it before M4.
4. **Suggestions only for local runtimes**, where local means the block's `host_id` equals the Rex server's. A Rex server on another machine therefore yields no suggestions even if paths coincide.
5. **Backends come from the config file, synced at startup by name**; removed backends stay as disabled rows so associations and history keep their references. No API to create or edit backends, because a backend makes the server run commands against a Rex server.
6. **Runtime state is not trusted across restarts.** On startup every `ONLINE` runtime becomes `UNKNOWN` until re-observed.
7. **Backoff 1 s doubling to 30 s**, reset after a stream that lasted 30 s; each reconnect starts with a full discovery to recover missed events.
8. **External ID format `session:<uuid>/block:<uuid>`**, because block methods need the session. If Rex ever moved a block between sessions, it would appear as a new runtime.
9. **`AgentAdapter` is an interface only**, with no implementation, as the spec asks for M0; it gains a Pi implementation in M4.

## Verification

- `go test -race ./...`, `go vet`, `gofmt`, stable `go generate`, `GOOS=darwin GOARCH=arm64 go build`: pass.
- Contract suite passes for the fake backend and for the Rex backend replaying recorded Rex output.
- Live, against your Rex server (read-only): `PRUNELLE_REX_LIVE=1 go test -run TestLiveRex ./internal/sessions/rex` discovers 4 terminal blocks and flags the Pi block (`harness_hint=pi`); through the built server, that block is suggested for a workspace on this repository and linked explicitly.
- Live lifecycle, against a separate throwaway Rex server (own socket and state dir, your sessions untouched): new session → `ONLINE`; its program killed → `STOPPED`; Rex server stopped → backend `UNREACHABLE`, runtimes `UNKNOWN`; Rex restarted → `REACHABLE`, `ONLINE`; association preserved; no `rex events` process left after Prunelle exits.
- Review routes keep working with the Rex backend pointing at a missing binary (API test).
- **Not verified:** Rex reached over Tailscale (`server = "https://…"`); a Rex server on another host (`local = false` path tested only with recorded data).
