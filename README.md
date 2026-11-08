# Prunelle

Review a feature, not merely its changed lines.

Prunelle is a feature-review tool: a Go review server and a desktop client. A review spans several Git repositories and is navigated by topics (intended behavior, observed changes, findings), each viewable as text, diagram, code diff, or evidence.

Status: early prototype. See [docs/plan.md](docs/plan.md) and the [spec](docs/Prunelle_Architecture_Implementation_Spec_2026-11-05.md).

## Layout

- `server/` - Go review server and `prunelle` CLI
- `client/` - React + Vite UI and MyGo desktop shell
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
allowed_roots = ["~/code"]        # repositories must live below one of these
allowed_logins = []               # Tailscale logins besides the machine owner
# data_dir defaults to ~/.local/share/prunelle (Linux) or ~/Library/Application Support/Prunelle (macOS)

# Optional: discover agent runtimes in Rex. Reviews work without it.
[[session_backends]]
name = "rex"
kind = "REX"
# rex_path = "rex"                # rex CLI
# server = ""                     # rex -S value; empty = default local server
```

Run the server and seed the demo review:

```sh
bin/prunelle serve
bin/prunelle fixture ~/code/prunelle-demo     # must be inside allowed_roots
bin/prunelle review ls
```

Expose it to your tailnet (no sudo once your user is the Tailscale operator):

```sh
tailscale serve --bg --https=443 http://127.0.0.1:7390
```

Requests through Tailscale Serve must come from the machine's Tailscale owner or a login in `allowed_logins`. Local processes on the server machine are trusted (see [decisions](docs/decisions.md), D2).

With a session backend configured, `prunelle sessions ls` lists discovered runtimes (one per Rex terminal block) and the workspaces they suggest; `prunelle sessions link RUNTIME_ID WORKSPACE_ID` records the association explicitly.

The API contract is [`server/api/openapi.yaml`](server/api/openapi.yaml). Review content is imported as a manifest: `prunelle review import REVIEW_ID manifest.json`; `prunelle fixture --print-manifest` prints a complete example.

Reviewers edit topics and representations, discuss, and decide through the API; every change is attributed to the caller (the Tailscale login, or the machine owner for local requests). An agent adds a `Prunelle-Agent: pi:<session-id>` header (the CLI sends `$PRUNELLE_AGENT`) so its changes are attributed to it; only humans submit decisions. Updates need the `ETag` they were based on, for example:

```sh
curl -X POST localhost:7390/v1/reviews/REVIEW_ID/discussions -H 'Content-Type: application/json' \
  -d '{"anchor": {"type": "TOPIC", "id": "TOPIC_ID"}, "body": "Why two retry loops?"}'
curl -X PATCH localhost:7390/v1/discussions/DISCUSSION_ID -H 'If-Match: "1"' -H 'Content-Type: application/json' \
  -d '{"status": "RESOLVED"}'
```

`prunelle events` follows changes as they happen (`/v1/events`, server-sent events).

## Desktop client (M2)

Requires Go, Node.js 26 and pnpm 12. On Linux, install GTK 3 and WebKitGTK 4.1 (`sudo apt install libgtk-3-0t64 libwebkit2gtk-4.1-0`). Packaged macOS builds target macOS 13.3+.

With the server running and a review seeded:

```sh
cd client
pnpm install --frozen-lockfile
pnpm build
pnpm desktop
```

Use **Server** in the app to connect to `http://127.0.0.1:7390` locally or `https://<machine>.<tailnet>.ts.net` through Tailscale Serve. The desktop fetches through Go; it adds no identity headers or credentials. The API server remains API-only, with no CORS exception.

- Navigate Topics, Findings, or All Source Changes (including Unassigned Changes).
- Switch Text / Diagram / Code / Evidence, or show two views. Evidence links and diagram nodes trace back to canonical records and stored source. Select line numbers to discuss an exact range.
- Discussions, replies, resolution and decisions are shared across views. Decisions name an exact revision; request changes selects actionable findings or threads. Comments never start an agent or change code.
- Perspectives and layout are local preferences. Capture is manual; source can be inspected at older revisions, while topic/representation metadata is the current graph (revision-aware comparison is M3).
- The sessions panel is optional, polls while open, and allows only explicit workspace association/removal. It has no process-control or agent-chat UI.

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

Both integration test commands build the real server and create disposable two-repository reviews. They never connect to the user's Rex server. `test:native` launches the bundled MyGo/WebKit app under Xvfb with isolated app storage and writes the ignored `client/native-smoke.png`. Its automation endpoint is compiled only with the `e2e` build tag, never into ordinary/release builds. The smoke test disables WebKit's sandbox only in that disposable test subprocess (for restricted CI containers).

The [M2 plan](docs/plans/m2.md) records implementation choices and verification.

## License

MIT
