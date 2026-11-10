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

**Supersession:** D12 retires this integration in M4. The paragraph above records the released M0b–M3 behavior, not a future dependency.

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

## D10. Reviews own repository scope; capture owns comparison inputs

**Decision:** retire workspace management and shared mutable repository configuration. Reviews are the product entrypoint; their repository membership and runtime associations are independent. Working folders are optional CLI context, not a second named object or a required discovery-root configuration. Explicit source paths/inline sets create reviews on demand; ambiguous cwd capture requires a review ID.

**Capture:** resolve per-repository override, capture-wide override, `[snapshot].default_base_ref`, then built-in `main`. Persist chosen refs/resolved SHAs in immutable repository snapshots, not as mutable workspace defaults. No fetch/branch switching or hidden fallback. Configuration reload requires restarting the server; committed idempotent captures remain replayable unchanged.

**Authorization:** the owner explicitly accepted that all authenticated callers, including `allowed_logins`, may request any Git source readable by the server account. Canonical Git validation remains, but there is no configured path allowlist. Authentication, attribution and human-only decisions are unchanged.

**Migration:** preserve historical repository IDs shared by old reviews through review-local memberships, rather than clone IDs or rewrite anchors. Migrate runtime links to every existing review that previously saw them. Retain old base preferences only as owned migration-audit metadata, never as capture defaults and remove workspace-only API/CLI contracts in a coordinated upgrade. The offline startup table rebuild validates foreign keys before commit and restores enforcement before serving; it is not a live request mode.

**Tradeoff:** there is no first-use heuristic for an unknown parent grouping. Target a multi-repo container explicitly; later cwd inference uses known review context. This avoids a parallel registry, namespace exclusions or arbitrary selection. Repository search/catalog and membership-editing UI are not added.

## D11. Capture freezes predecessor semantic state; re-review never guesses history or anchors

**Decision:** the latest source revision has an editable semantic graph. The next successful capture archives its predecessor's normalized graph in the same SQLite transaction as source insertion/current advance. Older graphs are read-only; new imports into them are rejected, while committed retries replay unchanged. Human edits and canonical identities carry forward. Replies, thread status and human decisions remain continuing activity against original revisions.

**Meaning:** comparison uses the predecessor's last metadata before advance, not a per-edit or per-comment semantic checkpoint. Topics remain `ACTIVE`/`OBSOLETE`; finding `RESOLVED`/`DISMISSED`/reopening is explicit and independent of thread resolution and verdicts. Similar titles/manifest keys never reconcile identities automatically.

**Source scope:** revision/file-scoped explicit topic sets override evidence-derived grouping, including an explicit empty set and an explicit reset. One domain rule supplies Files, topic code selection and original-thread context. Only evidence from that source revision derives ownership there. Copy manual corrections only for exact surviving repository/path identities; a disappearing path's correction is historical, not resurrected if the name returns later.

**Safety/tradeoff:** migration 0006 is additive with FK enforcement on. Unrecoverable pre-M3 semantic state is unavailable, never backfilled from today's graph; source/messages/decisions survive. Code anchor validity compares stored blobs on the same path/side without relocation. Semantic anchors open their original revision's recorded state; missing elements/context are flagged rather than replaced with a current node. No per-edit semantic reconstruction, background mapper or analyzer is introduced.

## D12. M4 connects directly to existing Pi and retires runtime backends

**Status:** implemented on the M4 branch; validation and implementation ledger are in [the M4 plan](plans/m4.md). Merge, deployment and live-provider qualification remain separate.

**Decision:** a companion extension in the existing Pi conversation connects outward to Prunelle's existing authenticated API. Reuse canonical `discussion_id` for continuing threads and an explicit invocation ID for each request/result. Pi retrieves discussion history by ID when needed instead of receiving every earlier message again. No RPC child, new listener, terminal scraping or second credential is required. A fork-per-review option is future work, not a current setting or per-discussion fork mechanism.

**Attachment:** a unique matching connected Pi conversation attaches to the review automatically; zero candidates leaves it unattached and multiple candidates require selection. Match canonical review cwd scope only in a common filesystem namespace. Manual choices take precedence; existing bindings and in-flight targets are never silently replaced. Attachment does not dispatch work.

**Content/actions:** first generate topics and explanations, not attention-only analysis or automatic findings. Explicit Prepare review publishes validated generated content directly; a topic-acceptance queue is not required. Preserve existing human content/grouping and canonical IDs under D11. Questions, generation and implementation are distinct explicit actions; merely saving a comment or decision invokes nothing. Use normal Pi permissions in the same conversation, not an asserted read-only sandbox. Only a deliberate human action sends a saved REQUEST_CHANGES decision as implementation work; replies/completion do not approve, resolve, capture or publish automatically.

**Retirement/tradeoff:** remove Rex and the otherwise-unused runtime-backend layer, including configuration, CLI/API/UI, observers and runtime records. This supersedes the original specification's Rex-first infrastructure requirement and D6. We lose independent terminal/process visibility when Pi disconnects; report unavailable/unknown rather than infer process death. Pi may still be hosted by Rex; Prunelle neither requires nor controls that host. Retire old runtime links through a new migration, never guess Pi identities from Rex IDs or rewrite released migrations. Stop/back up and coordinate upgrades as before; no user's actual Rex process or installation is removed.
