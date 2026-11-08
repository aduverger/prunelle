# M0a - Server foundation

Goal: a running Go server that registers a two-repository workspace, captures an immutable multi-repo revision, imports a review manifest, and serves the review read model over an OpenAPI-defined API. A fixture command builds a realistic workspace end to end. No Rex, no client, no mutations beyond creation/import.

## Scope adjustment

`docs/plan.md` placed Git capture in M1. Fixtures need real snapshots for Code views and unassigned-change detection, so capture lands here. M1 keeps idempotency keys, ETags, mutations, discussions, decisions, and SSE.

## Layout

```text
server/
  go.mod                       module github.com/aduverger/prunelle/server
  cmd/prunelle/                CLI: serve, workspace add, review create|capture|import, fixture
  api/openapi.yaml             contract (OpenAPI 3.1)
  api/                         oapi-codegen config + generated server/client/types
  internal/config/             config file, data dir, defaults
  internal/store/              SQLite open (writer + readers), migrations, queries
  internal/git/                argument-safe Git runner, repository validation, snapshot capture
  internal/review/             domain services: workspaces, reviews, capture orchestration, manifest import, read model
  internal/httpapi/            handlers (strict server), auth middleware, error envelope
  internal/fixture/            fixture repos + manifest
```

## Data model (migration 1)

Normalized tables with foreign keys; IDs are prefixed UUIDv7 strings (`ws_`, `repo_`, `rvw_`, `rev_`, `top_`, ...); timestamps RFC 3339 UTC.

- `workspaces`, `repositories` (workspace, label, canonical root, base ref, include_untracked; unique per workspace on label and root).
- `reviews` (workspace, title, description, status, current revision).
- `revisions` (review, ordinal, captured_at, fingerprint, status `CONSISTENT`/`INCONSISTENT`).
- `repo_snapshots` (revision, repository, base ref, base SHA, head SHA, branch, dirty counts, fingerprint, status, warnings).
- `snapshot_files` (snapshot, path, old path, status `ADDED`/`MODIFIED`/`DELETED`/`RENAMED`/`UNTRACKED`/`TYPE_CHANGED`, base/head mode, base/head Git OID, base/head blob, binary, content_omitted reason).
- `blobs` (sha256, size, content).
- `topics` (review, parent, title, summary, origin, state, position, author).
- `intents`, `observed_changes`, `findings`, `evidence` (+ `evidence_code_ranges`), `attention_signals`.
- Typed relations: `topic_intents`, `topic_changes`, `topic_findings`, `topic_evidence`, `change_evidence`, `finding_evidence`, `intent_evidence`, `attention_evidence`.
- `representations` (topic, revision, type, title, config JSON, origin).
- `imports` (review, revision, author kind/id, imported_at) referenced by imported rows for provenance.

Discussions, decisions, events, and session tables arrive in later migrations.

## Canonical rules

- **Topic → files:** a changed file belongs to a topic iff a `CODE_RANGE` evidence reachable from the topic (direct link, or through a linked change, finding, or intent) points at it. **Unassigned changes** = changed files of the revision belonging to no topic. One predicate serves Source Changes, Unassigned, and default Code views.
- **Representations:** `TEXT`, `DIAGRAM`, `CODE_DIFF`, `EVIDENCE_LIST` are stored rows. `CODE_DIFF`/`EVIDENCE_LIST` configs optionally narrow to listed evidence; empty means everything reachable from the topic. Availability is computed on read; MVP types without a row are returned as unavailable with a reason.
- **Author and provenance:** every record stores its author (the manifest author) and the provenance the author declares (`MEASURED`/`OBSERVED`/`INFERRED`/`UNKNOWN`; `UNKNOWN` when omitted, `OBSERVED` for code ranges). No rewriting by author kind (D8).

## Git capture

- Repository registration: absolute path, symlinks resolved, must be inside a configured allowed root; replaced by `git rev-parse --show-toplevel`, which must also be inside an allowed root.
- Git runs as `git -C <root> ...` via `exec.Command` (no shell) with `GIT_OPTIONAL_LOCKS=0`, `LC_ALL=C`, `-c core.fsmonitor=false`, `--no-ext-diff --no-textconv`.
- Base = `git merge-base HEAD <base_ref>`. Changes: `git diff --raw -z -M <base>` (committed + staged + unstaged) plus `git ls-files --others --exclude-standard -z` when untracked files are included.
- Base contents via `git cat-file --batch`; head contents read from the worktree with `Lstat`; symlinks stored as their target (Git semantics), never followed; parent directories must resolve inside the root.
- Head Git OIDs computed in Go for the repository's object format.
- Binary (NUL in first 8000 bytes) or > 1 MB: listed, content omitted, warning. New blob bytes > 50 MB per revision: capture fails.
- Consistency: after reading, re-check `HEAD`, the change list, and worktree content hashes. On mismatch retry (3 attempts), then store with status `INCONSISTENT` and a warning.

## API (M0a subset)

```text
GET  /v1/health                         (no auth)
GET  /v1/workspaces            POST /v1/workspaces        GET /v1/workspaces/{id}
GET  /v1/reviews?workspace_id  POST /v1/reviews           GET /v1/reviews/{id}
POST /v1/reviews/{id}/revisions         capture a new revision
GET  /v1/reviews/{id}/revisions         GET /v1/revisions/{id}
GET  /v1/revisions/{id}/source-changes  files per repo, linked topic IDs, unassigned flag
GET  /v1/revisions/{id}/file?repository_id&path   file entry with base/head text
POST /v1/reviews/{id}/imports           import a manifest into a revision
GET  /v1/reviews/{id}/topics            flat list ordered by parent/position
GET  /v1/topics/{id}                    topic + linked intent/change/finding/evidence IDs
GET  /v1/topics/{id}/representations    stored + unavailable entries
GET  /v1/reviews/{id}/intents | changes | findings | evidence | attention
GET  /v1/evidence/{id}
```

Errors: `{"error": {"code", "message", "details"}}`. Generated code is committed; CI fails if regeneration changes it.

## Access

Middleware on every route except `/v1/health`:
- `Tailscale-User-Login` present → must be the machine owner (from `tailscale status --json`, cached) or in `allowed_logins`; else 403.
- Absent → owner (loopback-only listener, see D2).

## Config

`<user config dir>/prunelle/config.toml`: `listen` (default `127.0.0.1:7390`), `data_dir`, `allowed_roots`, `allowed_logins`. Flags override. Data dir default: `$XDG_DATA_HOME/prunelle` on Linux, `~/Library/Application Support/Prunelle` on macOS.

## Fixture

`prunelle fixture --dir DIR [--server URL]` creates repos `api` and `worker` (deterministic commits on `main`, a `feature/async-processing` branch, committed + staged + unstaged + untracked changes, one changed file linked to no topic), then through the API registers the workspace, creates a review, captures a revision, and imports the embedded manifest: 3+ topics (one child topic, one UI-flow topic), 2 intents, 3 changes, 2 findings, code/test/spec evidence, Text/Diagram/Code/Evidence representations, one attention signal.

## Tests

- Store: migrations from empty; foreign keys enforced.
- Git: capture of committed/staged/unstaged/untracked/deleted/renamed/binary/symlink files; base = merge-base; path validation (outside root, symlink escape); inconsistency detected via an injected mutation between read and verify.
- Import: reference resolution, unknown refs, code ranges outside file bounds, agent provenance downgrade, many-to-many links not duplicated.
- API (httptest + generated client): fixture end to end; one review across two repos; topic link IDs stable; unassigned file listed; old revision content unchanged after worktree edits and a second capture; auth (owner, allowed, rejected, no header); error envelope.

## CI

GitHub Actions on Linux: `gofmt` check, `go vet`, `go test ./...`, generated-code check, `GOOS=darwin GOARCH=arm64 go build`.

## Out of scope

Mutations (topic edit, discussions, decisions), idempotency keys, ETags, SSE, Rex, client.

## Implementation notes

- Route for capture is `POST /v1/reviews/{id}/revisions` (creates a revision) rather than the spec's illustrative `/capture`.
- `GET /v1/reviews/{id}/imports` was added so clients can show who authored imported records (`import_id` on each record).
- Go domain types are the wire types: OpenAPI schemas map to them with `x-go-type`. Requests are validated against the spec by middleware (unknown fields rejected); API tests validate every response against the spec.

## Decision ledger

Choices made where the spec and plan were silent, least confident first.

1. **Provenance defaults:** `UNKNOWN` when omitted, except `OBSERVED` for code ranges, which point at captured code.
2. **Code and Evidence views are derived when not declared.** Every topic with reachable code ranges gets a Code view (`origin=DERIVED`, no ID) even if the manifest did not list one; Text and Diagram are never synthesized. *Would have asked:* is a derived view acceptable under "do not fabricate representations"? It only re-presents canonical links.
3. **Topic → file rule.** A file belongs to a topic through CODE_RANGE evidence linked directly or via the topic's intents, changes, or findings. Parent topics do not inherit their children's files. *Would have asked:* should a parent topic's Code view include its children?
4. **Code ranges must target changed files.** Only changed files are stored, so evidence pointing at unchanged code is rejected. *Would have asked:* is context evidence on unchanged files needed before M3?
5. **Imports are additive.** Re-importing the same manifest duplicates records; idempotency arrives with M1's `Idempotency-Key`.
6. **Omission is all-or-nothing per file.** If either side is binary, over 1 MB, a submodule, or not a regular file, neither side's content is stored.
7. **The 50 MB revision cap counts bytes before blob deduplication.**
8. **Git edge cases:** unmerged entries map to `MODIFIED`; nested repositories in the untracked list are skipped with a warning; detached `HEAD` yields an empty branch.
9. **Tagged server machine has no owner.** Only `allowed_logins` are admitted; the owner is looked up with `tailscale status --json` (CLI, not the Tailscale Go library, to avoid a large dependency) and retried at most once a minute.
10. **Enumerations I chose:** change kinds, finding kinds, evidence kinds, diagram node kinds (`COMPONENT`, `SERVICE`, `DATASTORE`, `QUEUE`, `ACTOR`, `STEP`, `STATE`, `OTHER`).
11. **Mechanics:** prefixed UUIDv7 IDs (`top_…`); migrations via `PRAGMA user_version` with embedded numbered SQL files instead of a migration library; TOML config; default port 7390; `~` expansion in config paths.

## Verification

- `go test ./...`, `go vet`, `gofmt`, stable `go generate`, `GOOS=darwin GOARCH=arm64 go build`: pass.
- Manual: built binary, `prunelle fixture` against a running server, restart, data intact; non-loopback listen refused; repository outside allowed roots refused; proxied request without login refused; owner login (resolved from this machine's real `tailscale status`) admitted.
- **Not verified:** a real `tailscale serve` round trip (not enabled here, since it publishes the server on the tailnet); CI (no remote yet); running on macOS (cross-compiled only).
