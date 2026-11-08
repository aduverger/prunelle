# Decisions

Decisions that refine or depart from the [spec](Prunelle_Architecture_Implementation_Spec_2026-11-05.md). Newest last.

## D1. Desktop client is a web UI in a MyGo shell, not SwiftUI

**Spec:** §7 native macOS client in SwiftUI with embedded `WKWebView` for diffs/diagrams.
**Decision:** React + Vite UI rendered by the system webview inside a MyGo (Go) desktop shell.
**Why:** diff and diagram renderers are web-based anyway; one UI runtime makes the shared selection model trivial; the app builds and is testable from Linux; Mac/Linux/Windows from one codebase; shell and server share Go, so a later single app can embed the server in-process.
**Cost:** MyGo is pre-1.0; less native feel. Keep the shell thin so another shell (Electron, Wails, Tauri) or a browser can replace it.

## D2. Tailscale identity instead of an API credential

**Spec:** §9 single-user API credential stored in the Keychain.
**Decision:** the server listens on loopback only and is exposed through `tailscale serve`. The `Tailscale-User-Login` header must match the machine's Tailscale owner or a login allowed in the server config file. No bearer token.
**Consequence:** a loopback TCP listener cannot authenticate local callers, so every local process on the server machine is trusted, and loopback requests without proxy headers act as the owner. A browser page is not a trusted local process: to defeat DNS rebinding, direct requests must address the server as `localhost` or a loopback IP, and proxied requests as an IP or a `*.ts.net` MagicDNS name. Tailscale Serve always adds `X-Forwarded-*` headers but adds `Tailscale-User-Login` only for user identities, so a proxied request without a login (a tagged device) is rejected rather than treated as local. On a tagged server machine there is no owner; list your login in `allowed_logins`. If shared machines matter later, run Prunelle as its own tailnet node (embedded Tailscale with `WhoIs`).

## D3. SQLite via pure-Go driver

`modernc.org/sqlite` keeps the server cgo-free and cross-compilable to macOS. One writer connection, reader pool, WAL, short transactions. Revisit only for a hosted multi-instance deployment.

## D4. Snapshots store full contents, not diffs

Each revision stores base and head contents of changed files as content-addressed blobs. Diffs are computed on read. Anchors refer to file lines on one side plus a blob ID, never to hunks. Base = merge-base(`HEAD`, base ref).
**Why not Git hashes only:** uncommitted work has no Git object, agents rewrite history (old commits get pruned), and repositories move. Revision history (re-review, comments on old revisions, acceptance criteria 7-8) needs the content. Plannotator and CodeRabbit avoid storage because they review only the current state or rely on the Git host; Phabricator and GitLab store diff content. A later alternative with the same API: a Prunelle-owned Git repository per workspace holding snapshot commits, with only hashes in SQLite.

## D5. Review content enters through an imported manifest

No LLM runs inside Prunelle before M4. Topics, intents, changes, findings, evidence, representations and attention signals are imported from a JSON review manifest, typically written by the coding agent. Records are `origin=IMPORTED`.

## D6. Rex accessed through its CLI

Rex documents its API methods but not a wire protocol. The Rex backend runs `rex api call` and `rex events --json` without a shell, always with `--autostart=false`.

## D7. Git capture lands in M0a

**Plan:** `docs/plan.md` placed capture in M1.
**Decision:** fixtures need real snapshots for Code views and unassigned-change detection, so capture is part of M0a. M1 keeps idempotency keys, ETags, mutations, discussions, decisions, and SSE.

## D8. Actor and declared provenance, no collection method

**Spec:** §4 lists `collection method` on evidence; §9 asks for `MEASURED`/`OBSERVED`/`INFERRED`/`UNKNOWN` labels.
**Decision:** every review record carries its author (`HUMAN`/`AGENT` + ID) and the provenance the author declares. Prunelle does not rewrite provenance by author kind and keeps no collection method: it never runs tests or tools itself, so evidence always comes from an author, which the author field already states. "Who says it" (author) and "how they know" (provenance) are independent.

## D9. Desktop requests go through MyGo; browser development uses a Vite proxy

**Decision:** M2 keeps MyGo, pinned at v0.8.3 with matching runtime/fetch packages. Its official Go-backed fetch plugin supports both REST and streamed SSE, including cancellation. The shell restricts API requests to loopback HTTP(S) or `https://*.ts.net`, refuses redirects, and adds no authentication headers. Tailscale Serve still authenticates the user; the human-facing UI never sends `Prunelle-Agent`.
**Why:** reuse the existing API and Tailscale access policy without adding server CORS settings, a new credential, or a second review server. Development/Playwright browser requests go through a fixed Vite proxy target instead. Both use generated OpenAPI types and the same UI.
**Cost:** MyGo is pre-1.0. The bundled Linux/WebKit path is tested; macOS builds are cross-built but native launch/signing/notarization require a Mac. The frontend targets Safari 16.4+, so packaged macOS builds declare macOS 13.3+. No Electron fallback is needed for this slice.
