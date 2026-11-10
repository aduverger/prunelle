# Prunelle companion for Pi

Connect the **existing** Pi conversation to Prunelle. Tested against Pi **1.5.1**, Node **26**, extension **0.4.1** (unchanged 0.4 protocol). No RPC child, fork, terminal host, second listener or credential. This is a local package, not a published registry release.

## Install and connect

From a checkout of Prunelle:

```sh
pnpm --dir integrations/pi install --frozen-lockfile
pi install "$(pwd)/integrations/pi"
```

In the existing Pi conversation, run `/reload`, then `/prunelle status`. No separate connect command is normally needed. To reconnect, change servers, or disconnect:

```text
/prunelle connect http://127.0.0.1:7390
/prunelle disconnect
```

Loading the extension attempts a connection to `PRUNELLE_SERVER` or `http://127.0.0.1:7390`; failure is nonfatal. `/prunelle connect URL` selects another server. Only loopback HTTP(S) or HTTPS `*.ts.net` origins are accepted; redirects are refused. Use the same loopback server for automatic shared-filesystem matching. Tailnet connections require explicit review selection because identical remote paths do not prove a shared namespace.

The extension registers tools `prunelle_discussion`, `prunelle_context` and `prunelle_publish_review`. Keep these available if your Pi invocation restricts active tools. Provider/model access remains Pi's configuration; reported READY means the conversation reports idle, not independently verified provider health. No model runs merely because the extension connects or attaches.

## Actions and recovery

- Post a desktop comment or reply: Prunelle saves it, then sends it to the attached Pi without another click. The prompt includes the exact selected captured code (no surrounding lines), repository/file/side/line/revision/blob identity, and earlier thread messages for follow-ups. It does not include the whole review graph. Pi may retrieve more captured context if needed. The Prunelle server queues comments for the selected Pi, including while busy/offline/paused. With no attached Pi, the comment remains saved and unsent. Pending requests can be cancelled from Prunelle; switching the selected Pi cancels that review’s unsent entries instead of redirecting them. Direct API comment writes remain storage-only; `agent ask` explicitly dispatches saved messages from the CLI.
- Pi answers normally for discussion and implementation. The extension posts the final nonempty assistant text from that correlated run after `agent_settled`, excluding thinking, intermediate/tool output and aborted/incomplete/error responses. The model no longer calls `prunelle_reply` or supplies reply routing IDs. Generation still uses `prunelle_publish_review`.
- **Prepare review** authorizes validated direct publication of new topics, inferred Markdown explanations and eligible source associations. Existing metadata and explicit/manual grouping remain unchanged. No acceptance queue, generated verdict, fabricated test evidence or automatic findings.
- **Send implementation request** requires a saved human REQUEST_CHANGES decision and deliberately sends its exact revision, actionable IDs and original anchors. The live checkout may differ from captured source. Ordinary comments/verdicts never dispatch it.
- Pi retains normal tools/instructions/permissions. Discussion/generation instructions do not authorize code changes, but this is **not a sandbox**. Implementation completion does not automatically resolve concerns, decide, capture, commit, publish or deploy.

One active request per connection. The **server**, not this extension, owns a durable FIFO comment queue across reviews. It dispatches the next comment only after the current request is terminal and Pi reports ready. A stale/busy incoming dispatch is still rejected by the extension, never steered or queued inside Pi. Pending comments continue while the desktop is closed; generation and implementation remain deliberate, immediate-only requests. Unrelated local input, branch/model changes and session switching invalidate correlation rather than attributing an unrelated answer. A model/context change between readiness and dispatch rejects work. The exact reported model and pre-request Pi leaf identity are retained with the invocation; they are not a complete mirror of Pi history or a cross-request analysis cache.

Connection failure/restart leaves unavailable/UNKNOWN outcomes. Inspect the existing invocation and Pi conversation before a deliberate retry; reconnect with `/prunelle connect`. Uncertain dispatched work pauses the server queue until a human inspects Pi and resumes pending comments. Never-dispatched queued work survives restart; dispatched commands are never replayed, and failed writes are not automatically retried. Matching completion replay is deduplicated by invocation; a conflicting second result rejects. Review deletion removes owned records and rejects late publication, but does not stop Pi or erase its user-owned conversation history.

## Protocol

The [OpenAPI contract](../../server/api/openapi.yaml) owns wire schemas. Registration/state and a live command SSE stream use `/v1/agent-sessions`; binding and invocation creation use existing review scope. `/v1/events` remains durable refetch notification, not an execution queue. Both streams use the existing authenticated API.

Requests carry explicit `DISCUSS`, `PREPARE_REVIEW` or `IMPLEMENT` intent and immutable canonical targets. Dispatch checks the exact selected `agent_session_id`; a binding change fails rather than retargeting. Association changes use ETag/If-Match. Initiation uses existing committed POST receipts. DISCUSS acceptance returns QUEUED, even when dispatch follows immediately; query invocation state for current delivery. Queued records pin Pi/revision/message identity, while producer model/context/connection are recorded at actual dispatch. Queue inspection is `GET /v1/agent-sessions/{id}/queue`; human controls are `POST /v1/agent-invocations/{id}/cancel` and conditional `POST /v1/agent-sessions/{id}/resume-queue` using the session’s quoted `queue_version` as If-Match. Completion is transactionally hash-deduplicated, including replay after capture. Failed answer publication is reported UNKNOWN, with a Pi warning; the answer stays in the Pi conversation and no automatic retry runs. Upgrade server/desktop and reload the extension together for selected-line prompts and default comment dispatch. Default comment interaction itself needs no migration; the server-side queue requires migration 0008. Stop the server and back up the full data directory before upgrading. Queue cancellation/resume are Prunelle controls, not model tools.

`prunelle_context` reads only the requested captured revision, not live worktree bytes. For a file, supply both `repository_id` and `path`; output includes metadata and raw BASE/HEAD text. Read tools use 1-based **character** offset/limit, default 10,000 and maximum 12,000 Unicode characters, staying below Pi's 50KB tool limit without losing long source lines. No private review transcript is stored outside Prunelle-owned records; Pi's own history remains Pi-owned.

## Development

Requires Go/Git for the real-server round trip:

```sh
pnpm install --frozen-lockfile
pnpm peers check
pnpm format:check
pnpm check
pnpm test
```

Tests use disposable storage/Git, the actual Pi SDK and its controlled faux provider, plus the real Go server/CLI. They do not attach to live user sessions or use paid provider credentials. Interactive Pi installation, real-provider behavior, live tailnet and native macOS qualification remain separate release checks.
