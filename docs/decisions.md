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
**Consequence:** a loopback TCP listener cannot authenticate local callers, so every local process on the server machine is trusted, and loopback requests without Tailscale headers act as the owner. If shared machines matter later, run Prunelle as its own tailnet node (embedded Tailscale with `WhoIs`).

## D3. SQLite via pure-Go driver

`modernc.org/sqlite` keeps the server cgo-free and cross-compilable to macOS. One writer connection, reader pool, WAL, short transactions. Revisit only for a hosted multi-instance deployment.

## D4. Snapshots store full contents, not diffs

Each revision stores base and head contents of changed files as content-addressed blobs. Diffs are computed on read. Anchors refer to file lines on one side plus a blob ID, never to hunks. Base = merge-base(`HEAD`, base ref).

## D5. Review content enters through an imported manifest

No LLM runs inside Prunelle before M4. Topics, intents, changes, findings, evidence, representations and attention signals are imported from a JSON review manifest, typically written by the coding agent. Records are `origin=IMPORTED` with their author; agent-authored claims are `INFERRED`.

## D6. Rex accessed through its CLI

Rex documents its API methods but not a wire protocol. The Rex backend runs `rex api call` and `rex events --json` without a shell, always with `--autostart=false`.
