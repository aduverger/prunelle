# Prunelle

Review a feature, not merely its changed lines.

Prunelle is a feature-review tool: a Go review server and a desktop client. A review spans several Git repositories and is navigated by topics (intended behavior, observed changes, findings), each viewable as text, diagram, code diff, or evidence.

Status: early prototype. See [docs/plan.md](docs/plan.md) and the [spec](docs/Prunelle_Architecture_Implementation_Spec_2026-11-05.md).

## Layout

- `server/` - Go review server and `prunelle` CLI
- `client/` - React + Vite UI and MyGo desktop shell
- `integrations/pi/` - companion extension for the existing Pi conversation (Pi 1.5.1)
- `docs/` - spec, delivery plan, decisions, per-milestone plans

## Development

Requires Go (see `server/go.mod`) and Git.

```sh
cd server
go test ./...
go generate ./api   # after editing api/openapi.yaml
go build -o bin/prunelle ./cmd/prunelle
```

Configuration lives in `<user config dir>/prunelle/config.toml` (`~/.config/prunelle/config.toml` on Linux):

```toml
listen = "127.0.0.1:7390"         # loopback only
allowed_logins = []               # Tailscale logins besides the machine owner
# data_dir defaults to ~/.local/share/prunelle (Linux) or ~/Library/Application Support/Prunelle (macOS)

[snapshot]
default_base_ref = "origin/main" # omit for the built-in main default; no automatic fetch/fallback
```

Run the server and seed the demo review:

```sh
bin/prunelle serve
bin/prunelle fixture ~/code/prunelle-demo
bin/prunelle review ls
```

### Reviews, source context and snapshots

A review owns its repository set, revisions, semantic content and Pi attachment. There is no workspace registration or configured discovery root. Start from a Git repository/worktree, or explicitly target a folder whose immediate children are Git worktrees. Prunelle does not recursively collect an entire namespace. New reviews re-evaluate source independently; changing a folder does not rewrite another review’s declared set.

Install the CLI on your `PATH` so cwd means your project directory, not the Prunelle Go module:

```sh
# From server/:
mkdir -p "$HOME/.local/bin"
go build -o "$HOME/.local/bin/prunelle" ./cmd/prunelle

# First multi-repository review: target its container, not an unknown member:
cd ~/code/emidat/unblock_elems
prunelle review create "Missing Permission"
prunelle review capture

# Explicit targets and operations work from elsewhere:
prunelle review create --path ~/code/emidat/unblock_elems "Another review"
prunelle review create --repo api="$HOME/code/api" --repo web="$HOME/code/web" "Selected repositories"
prunelle review capture --base-ref origin/main REVIEW_ID
prunelle review capture --repo-base-ref REPOSITORY_ID=origin/develop REVIEW_ID
prunelle review delete --yes REVIEW_ID
```

Creation without `--path` can reuse one known containing source context, but never copies another review’s membership/settings. Without known context, an invocation inside a Git worktree selects that repository; choose the container explicitly for a first multi-repo review. An omitted title defaults to the source folder or repository labels. Inferred capture requires exactly one review matching cwd through its context/member paths, including closed reviews; zero/multiple matches require explicit selection. It never picks a global/latest review.

Base refs are capture inputs, not review-registration settings: per-repository override, then capture-wide `--base-ref`, then `[snapshot].default_base_ref`, then built-in `main`. Defaults are loaded at server startup and consulted for each capture; restart after editing configuration. Each repository stores the actual ref, merge-base/head SHAs and immutable source. Missing refs fail atomically; Prunelle never fetches, switches branches or guesses a fallback. Committed idempotent retries retain the original revision even after defaults change.

Deletion removes only that review’s Prunelle history, scoped receipts and links, plus unreferenced identities/blobs. It never changes project files/Git history or stops runtimes. Other reviews survive, even when they share paths or migrated source identities. A deletion notification and body-less unknown-outcome protection remain. Unsent desktop drafts stay in memory after remote deletion until deliberate navigation; copy text you want to retain before leaving.

**Trust boundary:** every authenticated Prunelle caller, including `allowed_logins`, is trusted to select any Git source readable by the server account. There is no path allowlist. Canonical/Git validation rejects invalid or retargeted sources; it is not filesystem authorization. Grant access only to trusted users.

**Upgrade from M2b:** stop the server and back up its data directory. Remove `allowed_roots`, `workspace_root_paths` and `[[workspace]]` settings; use `[snapshot]` above. Startup applies migration 0005 before serving requests, retaining review/repository/revision/anchor IDs and source history, separating memberships and mapping existing runtime links to the reviews that previously saw them. Old per-repository base preferences remain in `repositories.legacy_base_ref` for migration auditing, never as capture defaults. They are removed with the last owning source identity, not retained in a deletion-exempt sidecar; future captures use configuration/explicit overrides. The stopped-server backup preserves pre-migration values too. Old workspace-only receipts are retired, while review receipts and pending retry protection survive. Upgrade server, CLI and desktop together: workspace APIs/commands/DTO fields were removed. Old binaries cannot read the newer database; rollback requires restoring the backup.

**Upgrade to M3:** stop/back up the server data directory and upgrade server/CLI/desktop together. Additive migration 0006 stores normalized frozen semantic views, finding versions and file-topic corrections with normal FK enforcement. Earlier source revisions without recorded semantic history explicitly report it unavailable; their stored code, existing threads and decisions remain accessible. The current graph is the known baseline and freezes on its next advance. Semantic views record the last state before advance, not per-edit/comment-time snapshots. Older binaries reject the new schema; rollback requires the backup.

**Upgrade to M4:** stop the server, back up its complete data directory, remove `[[session_backends]]`, and coordinate server/CLI/desktop/extension versions. Migration 0007 adds Pi bindings/invocations and retires stored runtime backends, sessions, associations and their committed receipts. Existing review/source/semantic IDs and anchors survive; old runtime IDs are never converted into Pi identities. This does not access or uninstall Rex or stop any process. Older servers reject schema 7; rollback restores the stopped-server backup. The extension targets Pi 1.5.1; see [setup and protocol](integrations/pi/README.md).

**Upgrade for the Pi comment queue:** stop the server and back up its complete data directory before migration 0008, then upgrade server/CLI/desktop together. Pending comments now persist per Pi conversation and can run while the desktop is closed. Previously dispatched work is never replayed; restart uncertainty pauses delivery until a human resumes it. Old binaries reject schema 8; rollback restores the backup. The companion still uses Pi 1.5.1 and reports readiness without keeping its own queue.

Expose it to your tailnet (no sudo once your user is the Tailscale operator):

```sh
tailscale serve --bg --https=443 http://127.0.0.1:7390
```

Requests through Tailscale Serve must come from the machine's Tailscale owner or a login in `allowed_logins`. Local processes on the server machine are trusted (see [decisions](docs/decisions.md), D2).

### Existing Pi conversation

Install the [Pi companion extension](integrations/pi/README.md), then `/reload` the existing coding conversation. It automatically tries `PRUNELLE_SERVER` or `http://127.0.0.1:7390`; use `/prunelle status` to check. `/prunelle connect URL` is only needed to reconnect or change servers. Registration and attachment send no model work. A unique connected local cwd match attaches automatically; multiple matches require selection. Manual selection/disconnection persists, and an offline target is never silently replaced. Tailnet/unknown filesystem namespaces require manual selection.

```sh
prunelle agent ls
prunelle agent binding                    # cwd's sole review; or --review REVIEW_ID
prunelle agent select --review REVIEW_ID PI_ID
prunelle agent disconnect --review REVIEW_ID
prunelle agent auto --review REVIEW_ID
prunelle agent prepare --review REVIEW_ID
prunelle agent ask --review REVIEW_ID DISCUSSION_ID MESSAGE_ID
prunelle agent implement --review REVIEW_ID DECISION_ID  # preview only
prunelle agent implement --review REVIEW_ID --yes --agent-session PI_ID DECISION_ID
prunelle agent requests --review REVIEW_ID
prunelle agent get INVOCATION_ID
```

Only humans dispatch work; unset `PRUNELLE_AGENT` when acting as a reviewer. Posting a desktop discussion or reply saves it and asks the attached Pi automatically. The request includes the original captured file/path/side, **only the selected line or range**, and earlier thread messages for follow-ups. Pi answers normally; the extension returns its final answer to the original discussion. Comments for a selected Pi join its durable FIFO queue, including while busy, offline or paused. Without an attached Pi, the comment stays saved and visibly unsent. Direct API comment writes remain storage-only; CLI users dispatch saved messages with `agent ask`. **Prepare review** directly publishes validated topics/explanations/source links for eligible unassigned captured text; it does not overwrite existing content or explicit grouping. **Send implementation request** separately authorizes the exact saved REQUEST_CHANGES decision. These use the existing live Pi conversation and normal permissions, not a read-only sandbox. Completion never decides, resolves or captures automatically, nor commits, publishes or deploys project code.

Pi runs one request at a time. **Settings → Integrations → Pi queue** shows pending work across reviews and lets you cancel queued comments without deleting them. Changing/disconnecting the review’s selected Pi cancels its unsent queue entries; it never redirects them. Queued comments survive desktop closure and server restart, and continue after Pi reports ready. Uncertain dispatched outcomes pause the queue; inspect Pi, then explicitly **Resume queued comments**. This resumes only never-dispatched work, never the uncertain request itself. Generation/implementation still require readiness and an empty, unpaused comment queue. CLI dispatch prints the exact Pi/revision and a request key; retry only the unchanged action with `--key`, `--agent-session` and `--revision` as printed. A lost response is not authorization to repost the question or start a second invocation. Inspect `agent get` after disconnect/restart: UNKNOWN is not failure or proof that Pi stopped. Reconnecting can deliver never-dispatched queued comments, but never replays dispatched execution.

The API contract is [`server/api/openapi.yaml`](server/api/openapi.yaml). Review content is imported as a manifest: `prunelle review import REVIEW_ID manifest.json`; `prunelle fixture --print-manifest` prints a complete example. Imports append records to the **current** revision; an explicit `--revision` must still name that current revision. Manifest keys remain import-local, so keep canonical IDs and conditionally update links when continuing an existing topic/finding. Imports do not match by title/key or overwrite human metadata. Committed retries retain their original responses after capture advances.

Reviewers edit topics and representations, discuss, and decide through the API; every change is attributed to the caller (the Tailscale login, or the machine owner for local requests). An agent adds a `Prunelle-Agent: pi:<session-id>` header (the CLI sends `$PRUNELLE_AGENT`) so its changes are attributed to it; only humans submit decisions. Updates need the `ETag` they were based on, for example:

```sh
curl -X POST localhost:7390/v1/reviews/REVIEW_ID/discussions -H 'Content-Type: application/json' \
  -d '{"anchor": {"type": "TOPIC", "id": "TOPIC_ID"}, "body": "Why two retry loops?"}'
curl -X PATCH localhost:7390/v1/discussions/DISCUSSION_ID -H 'If-Match: "1"' -H 'Content-Type: application/json' \
  -d '{"status": "RESOLVED"}'
```

`prunelle events` follows changes as they happen (`/v1/events`, server-sent events).

## Desktop client

Requires Go, Node.js 26 and pnpm 12. On Linux, install GTK 3 and WebKitGTK 4.1 (`sudo apt install libgtk-3-0t64 libwebkit2gtk-4.1-0`). Packaged macOS builds target macOS 13.3+.

With the server running and a review seeded:

```sh
cd client
pnpm install --frozen-lockfile
pnpm build
pnpm desktop
```

Use **Server** in the app to connect to `http://127.0.0.1:7390` locally or `https://<machine>.<tailnet>.ts.net` through Tailscale Serve. The desktop fetches through Go; it adds no identity headers or credentials. The API server remains API-only, with no CORS exception.

- Navigate Topics, Findings, Files (including Unassigned Changes), or Comments. Source navigation uses Pierre Trees: arrow keys to navigate, Enter to open, and Ctrl/Cmd+F to search the focused tree. Deleted files have a separate tree so file/directory replacements remain inspectable.
- Source review uses a continuous Pierre CodeView with split/unified diffs and Highlights syntax coloring (JS fallback when WebAssembly/SIMD is unavailable). Use the gutter `+`, selected line numbers, or **Comment on lines…** to compose inline on an exact BASE/HEAD range. Comments navigate to their original immutable snapshot.
- Semantic topics retain Text / Diagram / Code / Evidence and one/two-view layouts. Evidence links and diagram nodes trace back to canonical records and stored source. Drafts and explicit retry identities survive virtualized view recycling; they remain in memory, with warnings before leaving the review.
- Discussions, replies and resolution are shared across views. **Review decision** opens a modal with revision-specific verdicts/history; request changes selects actionable findings or threads. Closing the modal keeps a dirty draft. Posting comments/replies asks the attached Pi for explanation, never implementation; resolving a thread or saving a verdict dispatches nothing.
- Capture is manual. The latest semantic graph stays editable; the next successful capture freezes its predecessor atomically with source advance. Selecting an older revision loads its frozen topics, findings, links and representations, not today's metadata. Compare against the preceding or another earlier revision; change reasons and linked-source changes are separate.
- Explicitly resolve, dismiss or reopen findings; this never automatically resolves threads or submits a verdict. Topics remain active/obsolete navigation, not automatically resolved concerns. **Edit file topics** sets a many-to-many topic set (including deliberate unassignment) or resets to evidence-derived grouping. Corrections survive capture only for exact surviving repository/path identities, never guessed renames.
- Discussions retain their original anchors. Changed stored content is flagged outdated; missing/omitted content is unavailable to compare, not proof of deletion or a fix. No line relocation is attempted. Code, reply, decision and correction drafts remain in memory across refresh/advance; a stale correction requires explicit reload/review. Perspectives and layout stay local preferences.
- The review bar shows Pi attachment. **Settings → Integrations** lists Pi conversations and requests, polls only while open, and permits selection, automatic attachment or deliberate disconnection. Comment delivery status appears beside each human message, without an extra preview; queued messages have a cancel control. **Send to Pi** is an explicit recovery action for unsent/older messages; **Prepare review** and saved-decision implementation retain their target/revision previews. Merely opening a preview dispatches nothing. Lost-response dispatch identity stays independent of comment/reply/verdict drafts.

Package the desktop app (`build/` is ignored):

```sh
pnpm package:desktop
# Cross-build an unsigned macOS app bundle from Linux:
pnpm package:desktop -platform darwin/arm64 -skip-dmg
```

Signing/notarization and a native macOS launch still need validation on a Mac.

### Client development and checks

```sh
# Browser mode is only for development/testing; Vite proxies a fixed API target.
PRUNELLE_SERVER=http://127.0.0.1:7390 pnpm dev
pnpm generate             # after changing the server OpenAPI contract
pnpm format:check && pnpm lint && pnpm test && pnpm build
pnpm exec playwright install chromium
pnpm test:e2e
# Linux native smoke test; also needs xvfb and xauth:
pnpm test:native
```

Both integration test commands build the real server and create disposable two-repository reviews. They never connect to the user's Pi/Rex sessions or paid models. Browser agent tests use controlled API peers; `integrations/pi` separately exercises the real Pi SDK with its faux provider and the real Go server/CLI. `test:native` launches the bundled MyGo/WebKit app under Xvfb with isolated app storage and writes the ignored `client/native-smoke.png`. Its automation endpoint is compiled only with the `e2e` build tag, never into ordinary/release builds. The smoke test disables WebKit's sandbox only in that disposable test subprocess (for restricted CI containers).

The [M2 plan](docs/plans/m2.md), [M2b plan](docs/plans/m2b.md), [M3b plan](docs/plans/m3b.md), [M3 plan](docs/plans/m3.md), [M4 plan](docs/plans/m4.md) [Pi comment simplification](docs/plans/pi-comments.md) and [server-side comment queue](docs/plans/pi-comment-queue.md) record implementation choices and verification.

## License

MIT
