# Prunelle - Feature Review Product and Implementation Specification

**Status:** Implementation-ready prototype brief (revised 2026-11-05)  
**Working name:** Prunelle  
**Audience:** Implementing coding agent / senior engineer  
**Architecture:** One Go review server, one native macOS client (SwiftUI), Tailscale connectivity; optional, pluggable session backends (Rex first)  
**Primary unit of navigation:** `ReviewTopic`  
**Primary product outcome:** Review a feature, not merely its changed lines.

## 0. Instructions to the implementing agent

Build a small working vertical slice before building a broad framework. Treat the **MVP contract and acceptance criteria** below as authoritative. This document specifies the desired abstractions, not an obligation to implement every future capability. Keep the server and client independently buildable. Record any changed decisions in `docs/decisions.md`. Do not silently substitute a diff-first or agent-chat-first product.

**First product milestone:** Given a workspace with two Git repositories, open one review, navigate its topics, select a topic, switch between a text explanation, a diagram, and the relevant code diff, inspect linked evidence/findings, comment on the topic or a code range, and switch between engineering and design perspectives. All changes persist after client/server restarts. No AI provider, Rex server, or running agent is required for this milestone. **Parallel P0 integration spike:** connect to an existing Rex server, discover a session and associate it with a Prunelle workspace without making review functionality depend on Rex availability.

## 1. Product thesis and differentiators

Prunelle helps humans understand, assess, and improve an entire software feature. A feature can span several repositories, behaviors, APIs, user flows, and architectural boundaries. Source files and lines are *evidence*, not the default mental model of review.

1. **Feature-first navigation:** Review **topics** represent meaningful aspects of a feature: intended behavior, an observed implementation change, or a concern discovered during analysis. Topics may be hierarchical and cross-repository.
2. **Many representations of the same subject:** For any topic, display an explanation, architecture/flow diagram, selected code diff, metrics, screenshot/preview, test evidence, or an agent-history view. These are interchangeable or side-by-side **views**, not independent review objects.
3. **Intent + implementation + findings:** Preserve what should happen (`Intent`), what was observed (`ObservedChange`), and what needs attention (`Finding`) as distinct linked records. Do not equate a topic with a finding.
4. **Trust-calibrated attention:** Show why topics may deserve scrutiny, with evidence and uncertainty. No unsupported numerical correctness/confidence scores, and never hide the exhaustive source changes.
5. **Persona-aware composition:** Engineering, design, and other perspectives change topic order, default views, and review prompts, not the underlying evidence or review state. Perspectives are not authorization roles.
6. **Native multi-repository workspaces:** One review can cover many repositories and, later, map to multiple remote PRs.
7. **One central review server:** Persistent reviews, revisions, topics, comments, and events shared among clients.
8. **Agent participation, not agent dependency:** The original coding agent can explain a topic and receive explicit change requests, but the review works without any agent. Do not reinvent an agent harness, terminal multiplexer, or execution-durability framework.
9. **Session-backend independence:** Connect to externally hosted agent sessions through **Rex first**, later Herdr or other runtimes; ultimately support Prunelle-managed sessions for customers without a multiplexer. Separate execution/session management from harness-level semantic interaction. The core review domain must not depend on Rex.

A reviewer should be able to answer: **What was intended? What actually changed? Why? Which assumptions/constraints were affected? What evidence supports the result? What merits human attention? What must change before approval?**

## 2. Core workflow

1. **Open a feature review.** The review includes repository comparisons and optional feature intent/design references.
2. **Navigate topics.** Start with a feature map that groups meaningful behaviors and changes; switch to Findings or All Source Changes when needed. Topics may have no original intent (e.g., an unexpected dependency).
3. **Inspect a topic using the best view.** Select Text, Diagram, Code, Evidence, or other available representations. Show multiple views simultaneously where useful.
4. **Trace claims to evidence.** Navigate from a diagram node or explanatory statement to an immutable source snapshot, test run, specification, or code range. An AI summary is not itself verification.
5. **Discuss.** Comment on a topic, finding, diagram element, or exact code range. If an agent is connected, optionally ask it to explain; commenting never automatically changes code.
6. **Submit a decision.** `COMMENT`, `APPROVE`, or `REQUEST_CHANGES`; selecting required findings/threads creates explicit implementation work. A human decides; an analyzer never auto-approves.
7. **Re-review a new revision.** Show changed/resolved/new topics and findings, preserve discussions, and flag obsolete anchors. A previous decision remains tied to the revision it reviewed.

**Navigation must not be linear.** A user can jump directly from a topic to a changed file, or start from a finding/source change and discover its topic. Always expose an `Unassigned changes` section so generated grouping cannot conceal code.

## 3. Conceptual example

Feature: "Process documents asynchronously" across `frontend`, `api`, and `worker`.

- **Intent I-1:** Upload returns promptly and users can follow processing status.
- **Observed change C-1:** New queue, background worker, status endpoint, and UI polling.
- **Topic T-1:** "Background processing", linking I-1 and C-1.
- **Finding F-1:** Retries might reprocess the same document without idempotency safeguards.
- **Evidence E-1:** Worker source snapshot; E-2: unit-test result; E-3: API contract diff.
- **Attention A-1:** Review priority HIGH because side effects plus uncertain retry behavior. It is *not* a claim that a bug is proven.
- **Views of T-1:** Explanation; queue/worker diagram; cross-repo code diff; test evidence. Each view references the same canonical records.
- **Discussion D-1:** Anchored to T-1 or to F-1, optionally with an exact source-range anchor. An agent may answer inside that thread.

The same topic defaults to architecture+code for an engineer, and status-flow+UI-state views for a designer. Findings and comments remain shared.

## 4. Domain model (canonical, separate from UI)

All entities have stable IDs and `created_at`/`updated_at` where meaningful. Use SQLite with foreign keys and migrations. The following is a logical schema; implement normalized tables/joins rather than JSON blobs for core relations.

| Entity | Essential fields / responsibility |
| --- | --- |
| `Workspace` | `id`, name, registered root, metadata; contains repository bindings. |
| `Repository` | `id`, workspace ID, canonical Git root, label, base ref/config; identity must not be a path alone. |
| `FeatureReview` | `id`, workspace ID, title, description, status (`OPEN`, `CLOSED`), current revision ID. |
| `ReviewRevision` | `id`, review ID, ordinal, captured time, fingerprint, status; immutable reference to per-repo snapshots. |
| `RepoSnapshot` | revision ID, repository ID, resolved base/head SHA, worktree state, diff artifact, changed-file snapshot metadata, capture warnings. |
| `ReviewTopic` | `id`, review ID, parent topic ID nullable, title, summary, origin (`MANUAL`, `IMPORTED`, `GENERATED`), lifecycle state, stable ordering; *navigational grouping*. |
| `Intent` | `id`, review ID, desired behavior/constraint, source/provenance, acceptance criteria optional. |
| `ObservedChange` | `id`, revision ID, kind, before/after claim, affected components, evidence refs, verification status. |
| `Finding` | `id`, review ID, first-seen revision, kind, severity, status (`OPEN`, `DISMISSED`, `RESOLVED`), rationale, evidence refs. |
| `Evidence` | `id`, revision ID, kind, source, immutable content/content reference, scope, collection method, provenance, availability. |
| `AttentionSignal` | `id`, revision ID, target ID/type, priority (`HIGH`, `MEDIUM`, `LOW`, `UNKNOWN`), rationale, source/analyzer, evidence refs, uncertainty; not a defect probability. |
| `RepresentationSpec` | `id`, topic ID, type, config payload, referenced entity/evidence IDs, generated/hand-authored origin, revision compatibility. |
| `Discussion` | `id`, review ID, primary anchor, status, messages; may reference a topic, finding or evidence. |
| `DiscussionMessage` | `id`, discussion ID, author kind/ID, text, timestamp, optional agent invocation ID. |
| `ReviewDecision` | `id`, review ID, revision ID, author, decision, selected actionable findings/discussions, notes, timestamp. |
| `Perspective` | preset ID, title, preferred topic ordering, block/view defaults, filters; server-shared preset or client-local customization. |
| `SessionBackendRegistration` | `id`, `kind` (`REX`, later `HERDR`/`MANAGED`), endpoint/config reference, enabled state, connection health; never persist access tokens in cleartext. Optional for standalone review. |
| `RuntimeSession` | Prunelle-owned ID, backend registration ID, opaque backend-specific session ID, workspace association, observed lifecycle state, discovered metadata and capabilities. Represents a runtime/session, **not** an agent conversation. |
| `AgentSession` | Prunelle-owned ID, harness kind (`PI`, later others), opaque harness conversation/session ID, optional runtime session ID, workspace ID, adapter kind, semantic capability set, availability. May exist without a multiplexer or runtime-session record. |
| `ReviewAgentBinding` | review ID (optionally topic/discussion ID), agent-session ID, role (`IMPLEMENTER`, `REVIEWER`), primary flag; one review may bind multiple agents. |
| `AgentInvocation` | ID, review/discussion ID, agent-session ID, optional execution-session ID, intent (`DISCUSS`, `IMPLEMENT`), mode (`LIVE`, `FORK` where supported), correlation ID and status; later milestone. |
| `AnalysisRun` | revision, provider ID/version, input fingerprint, status, outputs, errors, provenance; optional in MVP. |

**Relations are many-to-many.** A topic may link to multiple intents, changes, findings, and evidence; one finding may affect several topics. Store these as typed relations (e.g. `topic_links`, `finding_evidence`, `change_evidence`). Do not duplicate an intent or finding merely because two topics display it.

**Topic is not the universal data container.** It is a stable, human-understandable way to navigate canonical linked records. A finding can be discussed without belonging to an existing topic. A topic can exist without any finding. Multiple representations can present one topic, and a representation can refer to evidence relevant to several topics.

### 4.1 Review anchors

Represent anchors as typed targets with optional precise sub-location:

- `TOPIC`, `INTENT`, `CHANGE`, `FINDING`, `EVIDENCE`, `COMPONENT`, `USER_FLOW_STEP`, `DESIGN_ELEMENT`, `REVIEW`.
- `CODE_RANGE`: repository ID, revision ID, file path, side (`BASE`/`HEAD`), line start/end, selected text, nearby context, blob hash where available.
- `DIAGRAM_ELEMENT`: representation ID, stable element ID; links back to a canonical topic/change/component/evidence wherever possible.

A comment belongs to one `Discussion`; its anchor is independent of whichever block displayed it. A topic discussion must be visible regardless of whether the user has selected Diagram or Code.

Anchors refer to the revision they were made against. On later revisions, retain original location and optionally compute a new mapped location with confidence/ambiguity; mark outdated when uncertain. Never silently reanchor to incorrect code.

### 4.2 Revisions and user edits

`ReviewRevision` is immutable. Repo snapshots and analysis artifacts are versioned. Human-authored topic titles/order, relationships, and discussions persist separately, with change history where practical. Regenerating AI proposals **must never overwrite manual edits**; offer additions/corrections for acceptance. Review decisions are tied to exact revisions.

### 4.3 Session identity, provenance, and binding

The review domain references **Prunelle IDs**, not Rex pane IDs or Herdr process IDs. A backend may recycle names/IDs or become unreachable; retain `backend_id` plus the opaque external ID for lookups and reconnects. A harness session identity (Pi conversation ID) differs from the terminal/runtime session identity (Rex block/session). The binding between them is explicit and may be absent or stale.

- `SessionBackendRegistration` describes **how** Prunelle discovers/controls execution sessions. Example: `rex-personal` with a local or tailnet-private endpoint. It is configuration, not a hard dependency.
- `RuntimeSession` is an observed process/terminal/session in that backend. Use provider-scoped opaque external IDs; distinguish `ONLINE`, `OFFLINE`, `STOPPED`, and `UNKNOWN` rather than assuming a disappeared Rex connection means the agent stopped.
- `AgentSession` is a **logical agent conversation** with a harness ID, capabilities, and an optional runtime link. It can outlive a runtime connection and may be directly attached over the harness protocol without a multiplexer.
- `ReviewAgentBinding` records which agent participated in the feature and which one should receive review requests. It must be explicit, editable, and review-scoped; never infer implementer identity solely from matching working directories.
- `AgentInvocation` stores asynchronous and correlated interactions, preserving the original thread, source session, and distinct execution session if forked.

No schema changes should be necessary to add a second external backend or a managed backend. Backend-specific configuration and metadata may live in typed extension records, but review/topic/finding/comment tables must not carry Rex-specific fields. Linking a new backend does not migrate an already running process; runtime mobility is not guaranteed.

## 5. View and block model

**`ReviewTopic` = what the user navigates. `RepresentationSpec` = how that topic can be expressed. `ViewBlock` = where/how it renders in the client.** They are distinct.

### 5.1 MVP representation types

- `TEXT`: title, structured explanation sections, links to evidence and claims.
- `DIAGRAM`: structured nodes/edges (component/behavior/flow identifiers), labels and links to canonical review entities. Use a constrained renderer; avoid arbitrary executable HTML from model output.
- `CODE_DIFF`: repository/file/range filters referencing immutable review snapshot; can span multiple repositories.
- `EVIDENCE_LIST`: tests, analysis output, specs, collected artifacts, provenance and unknowns.

Future: `METRIC`, `IMAGE_COMPARISON`, `USER_JOURNEY`, `API_CONTRACT`, `AGENT_TIMELINE`, `ARCHITECTURE_MAP`. Do not fabricate these when required inputs are missing.

For each representation, return `available`, `unavailable_reason`, data requirements, and evidence references. Some topics may have only Text+Code; do **not** generate a diagram simply to fill a tab.

### 5.2 Client block model

A block has `instance_id`, `kind`, `topic_id` or review scope, `representation_id`, placement, and local presentation configuration. Blocks are **renderers**, not separate sources of truth and not server-side execution contexts. A topic can be shown in two blocks simultaneously (e.g., Diagram and Code). A common selected-context model propagates `topic_selected`, `finding_selected`, `evidence_selected`, and `code_range_selected` across blocks.

Provide flexible layout with SwiftUI split panes first; arbitrary drag/drop grids and code-generated UI are out of MVP. A native SwiftUI shell may embed a `WKWebView` for diff rendering (e.g., Pierre Diffs) or diagrams where reuse is valuable. The server protocol stays UI-technology-neutral.

### 5.3 Perspectives

Perspectives configure **presentation**, not identity or permission:

- **Engineering:** topic map, findings, architecture/flow diagram, code diff, evidence/quality.
- **Design:** intent/user flow, text/design representation, screenshots when available, relevant findings.
- **Source changes:** exhaustive repository/file list, code diff, unassigned changes.

For MVP ship `Engineering`, `Design`, and `Source` presets. A design preset must still be useful if there are no screenshots, without claiming visual validation occurred. Persist local selected perspective and block layout; keep discussions and review decisions global.

Do not optimize for a specific persona by hiding evidence from others. Topic ordering can be suggested by attention signals, but also offer manual order and an exhaustive view.

## 6. Backend - one Go service

Responsibilities: workspace registration, Git diff/snapshot collection, review/topic storage, representations, evidence, comments, decisions, event streaming, an optional **session-backend registry**, and optional harness adapters/analyzers. Ship as one Go binary with SQLite WAL and migrations. Prefer a small set of packages (`api`, `store`, `git`, `review`, `events`, `sessions`, `agents`) over microservices or a speculative plugin platform. Rex remains a **separate existing server**; the Prunelle Go service is its client and the sole authority for review state.

### 6.1 Git workspace capture

Each repository has its own configured base ref and comparison mode. For MVP support `base-ref -> current working tree` (including committed, staged, and unstaged tracked changes); indicate untracked files explicitly and optionally include them through a documented setting. Resolve and store a base commit, HEAD SHA, per-repo status, changed-file contents/hashes or immutable diff artifacts, and any capture warnings. A review revision is a **logical cross-repo snapshot**, not a claim of atomicity across independent Git worktrees. Detect if repository state changes during capture; retry or mark the snapshot inconsistent rather than silently claiming it is stable.

Do not rely on current filesystem content for rendering an old revision. Preserve enough diff and changed-file content to view older revisions. Use Git through argument-safe process execution, scoped to registered canonical repository roots. No arbitrary commands from clients.

### 6.2 Minimal API sketch (`/v1`)

```text
GET    /v1/workspaces
POST   /v1/workspaces
GET    /v1/workspaces/{id}
POST   /v1/reviews
GET    /v1/reviews
GET    /v1/reviews/{id}
POST   /v1/reviews/{id}/capture
GET    /v1/reviews/{id}/revisions
GET    /v1/reviews/{id}/topics?tree=true
POST   /v1/reviews/{id}/topics
PATCH  /v1/topics/{id}
GET    /v1/topics/{id}
GET    /v1/topics/{id}/representations
POST   /v1/topics/{id}/representations
GET    /v1/reviews/{id}/findings
GET    /v1/reviews/{id}/source-changes
GET    /v1/reviews/{id}/evidence/{evidenceId}
GET    /v1/reviews/{id}/discussions
POST   /v1/reviews/{id}/discussions
POST   /v1/discussions/{id}/messages
POST   /v1/reviews/{id}/decisions
GET    /v1/session-backends               # optional; P0 Rex connection
GET    /v1/runtime-sessions?workspace_id=... # optional; provider-neutral
GET    /v1/agent-sessions?workspace_id=...   # future semantic discovery
POST   /v1/reviews/{id}/agent-bindings     # later; explicit association
GET    /v1/events?after={sequence}
```

The routes are illustrative; choose a consistent REST or typed RPC scheme and document it in an OpenAPI spec. Session routes must use Prunelle-owned IDs and never expose an unscoped arbitrary command executor. Maintain stable IDs and explicit `review_revision_id` parameters when reading revision-specific artifacts. Prefer structured error responses and optimistic concurrency (`version`/`If-Match`) on mutable review content. Accept idempotency keys for capture, comment creation, and review decisions. Stream ordered, resumable events (SSE is enough for MVP); on reconnect, refetch authoritative state and resume after the last sequence.

### 6.3 Session backends are optional infrastructure (Rex first)

Session backends provide **runtime/session lifecycle and discovery**, not semantic access to a coding-agent conversation. A backend is a separate dimension from the harness: Pi may be hosted by Rex, Herdr, or a Prunelle-managed process. The initial Go adapter is `RexSessionBackend`. Do not assume every provider exposes terminal attachment, start/stop, or even a PTY.

Use small, capability-aware interfaces (illustrative Go; adapt to verified provider APIs):

```go
type SessionBackend interface {
    Kind() string
    Discover(ctx context.Context) ([]RuntimeSession, error)
    Inspect(ctx context.Context, externalID string) (RuntimeSession, error)
    Subscribe(ctx context.Context) (<-chan RuntimeEvent, error)
}

// Optional capability, NOT a method required on every backend.
type SessionLauncher interface {
    Start(ctx context.Context, req StartSessionRequest) (RuntimeSession, error)
    Stop(ctx context.Context, externalID string) error
}

// Optional capability when a provider exposes interactive terminal attachment.
type TerminalAttacher interface {
    Attach(ctx context.Context, externalID string) (TerminalStream, error)
}
```

Provider capabilities must be explicitly reported. The same API may return `UNSUPPORTED` for provider-inapplicable operations. Preserve the distinction between Rex's session, block, and terminal/process identifiers; normalize them into provider-neutral runtime descriptors only after validating the actual Rex API. Subscribe/reconnect should be bounded, cancellable, and recover from lost events through a fresh discovery snapshot. Do not infer that stream attachment implies agent-history access.

**First provider: Rex.** For P0, implement a small client that connects to a configured Rex server, lists/inspects sessions and relevant blocks, observes status changes, and associates an existing runtime with a workspace. It should work over a local protected channel or through tailnet-restricted Rex connectivity; where the servers share a machine, prefer local/private access. Store an explicit mapping and handle offline/stale sessions gracefully. Verify the exact Rex protocol/version and permissions during the spike instead of assuming that every documented method works identically across releases.

**Future providers:** `HerdrSessionBackend` implements the same lifecycle/discovery contract. `ManagedSessionBackend` lets Prunelle launch/supervise supported harness processes directly without requiring Rex, Herdr, tmux, or another terminal multiplexer. Prefer a harness-native RPC/headless interface where feasible. A PTY is an implementation detail only if a particular agent requires one. Process supervision does **not** imply durable, checkpointed agent execution after crashes.

**Important:** Whether Rex is configured or reachable must not prevent the Go API from starting, the macOS client from opening reviews, or a reviewer from creating comments and decisions. A fake/in-memory backend should validate the contract in tests; do not implement Herdr or managed sessions in P0.

### 6.4 Agent harness adapters are a separate layer

`AgentAdapter` understands **agent semantics** (history, messages, tool events, interruptions), independently of the `SessionBackend` managing the process:

```go
type AgentAdapter interface {
    Kind() string
    GetHistory(ctx context.Context, session AgentSessionRef) ([]AgentMessage, error)
    Send(ctx context.Context, session AgentSessionRef, message AgentMessage) (InvocationRef, error)
    Subscribe(ctx context.Context, session AgentSessionRef) (<-chan AgentEvent, error)
}

// Optional harness capabilities: Interrupt, Fork, Start, etc.
```

Start with Pi only when agent integration begins. It should talk to Pi's semantic control interface or companion extension, ideally targeting the **same Pi conversation** that runs within Rex. Rex supplies runtime discovery/lifecycle, not Pi history, messages, tool calls, or review-thread correlation. The adapter may connect directly to Pi using an authenticated socket/protocol; it does not have to tunnel semantic messages through Rex's terminal stream. Also support future directly managed harness processes with no terminal session record. Correlate review discussions using invocation/thread IDs, never message arrival order.

### 6.5 Analysis pipeline (later milestone)

Providers may derive semantic changes, attention signals, metrics, and candidate findings from Git, AST parsing, tests, specs, and LLMs. Every output must record producer, version, input revision, provenance, and whether it is measured, observed, inferred, or unknown. Keep model suggestions reviewable, not automatically authoritative. Cache by input fingerprint. Never implement a black-box aggregate quality score as the MVP.

## 7. Native macOS client

Use SwiftUI for navigation, split panes, toolbar, perspective switcher, discussion UI, selection and shortcuts. Prefer native views where reasonable; use a contained `WKWebView` for high-quality code diffs rather than rebuilding a complete editor engine.

**Primary layout:** left = review topic tree/findings/source tabs; center = selected topic header and configurable representations; right/bottom = evidence, discussion, or inspector. Allow switching Text/Diagram/Code/Evidence for a topic, as well as showing two concurrently. All representations use the same selected topic and canonical discussion IDs. Ensure accessible keyboard navigation and useful empty/loading/error states.

The first version is a desktop application; later clients can reuse the server API. Native Swift is a prototype choice, not an API constraint.

## 8. Comments, decisions, and agent/session integration

**Review domain comes first.** Comments are conversational and **do not authorize code changes**. Review decisions are explicit and tied to a revision: `COMMENT`, `APPROVE`, `REQUEST_CHANGES`. A `REQUEST_CHANGES` decision identifies actionable findings/discussions and can produce a structured implementation request if an agent is connected. Neither Rex nor Pi is required for comments or approvals.

### 8.1 Two orthogonal pluggable interfaces

```text
Prunelle macOS client ─── Review API ─── Prunelle Go server / SQLite
                                             │
                                   Workspace / Review / Topics
                                             │
                         ┌───────────────────┴────────────────────┐
                         │                                        │
                  SessionBackend                           AgentAdapter
              execution/lifecycle                     semantic conversation
                         │                                        │
             ┌───────────┼───────────┐                    ┌───────┼───────┐
             │           │           │                    │       │       │
            Rex        Herdr    Prunelle-managed         Pi     Claude   Codex
           (P0)       (later)       (later)           (first)  (later)  (later)
```

This diagram shows **independent axes**, not a fully supported matrix. A runtime backend may be unavailable, and an agent adapter might connect directly to an already running agent. The user can always review a feature without either.

**SessionBackend** discovers, inspects, subscribes to, and (when supported) starts/stops execution sessions. **AgentAdapter** provides structured history, send, subscribe, interrupt, and optionally fork/start for a specific coding harness. Neither interface should require a terminal byte stream, and the two must not be conflated. The Go service coordinates them, while the macOS client speaks to Prunelle's API; it need not connect directly to Rex to use review functionality.

### 8.2 Deployment modes

- **Rex-backed (first):** User already runs Pi or another harness under Rex. Prunelle discovers the runtime and binds the agent session to a review, while the harness adapter communicates semantically with the agent. Rex continues to own its sessions/processes.
- **Herdr-backed (future):** Same review and agent abstractions, but lifecycle/discovery comes from Herdr. No product-level migration required.
- **Prunelle-managed (future):** For users with no multiplexer, Prunelle launches and supervises an agent process using a supported harness protocol, reconnects clients, and persists metadata. This is process/session management, **not** a promise of durable agent execution. Do not make Rex a customer prerequisite.
- **Review-only:** No session backend or agent session. All review/topic/comment/decision features remain available.

### 8.3 Binding a coding agent to a review

Persist `RuntimeSession`, `AgentSession`, and `ReviewAgentBinding` separately (section 4.3). One workspace may have multiple runtime sessions; one review may have an implementer and optional review agents. Always show which agent session is linked and its provenance. Avoid silently matching by cwd alone, and allow the user to correct or remove a binding. The harness conversation/history is owned by the harness, not by Rex; Prunelle stores associations and review-specific discussion state.

### 8.4 Live versus forked conversations (open decision)

- `LIVE`: send a discussion to the implementer's active agent session. Preserves history; concurrency and interference must be handled.
- `FORK`: create a separate review conversation from the original agent history when the harness supports it. Isolates discussion but risks context divergence and requires explicit implementation handoff.

Do not commit to universal fork support. Never correlate concurrent inline discussions by temporal ordering; preserve invocation, thread and source-session IDs, and use structured responses or an agent tool. A comment is never treated as an implementation command merely because an agent answered it.

### 8.5 Session ownership is not execution durability

Rex/Herdr may keep a process available after a client disconnects. A future Prunelle-managed backend may supervise/restart processes. None of these automatically recovers an in-flight tool call or checkpoints the agent's reasoning/execution. Prunelle persists reviews and discussions independently; full harness durability is outside P0 and the initial product scope.

## 9. Trust, security, and failure behavior

- Tailscale connects macOS client to the private Prunelle server. Rex is a separate, optional and potentially privileged service; prefer private/local server-to-Rex connectivity and restrictive tailnet policy if reached remotely. For MVP add a single-user API credential stored securely (macOS Keychain); do not equate tailnet reachability with fine-grained authorization.
- Canonicalize registered roots and validate repository membership; prevent path traversal, symlink escape, shell injection, arbitrary client-triggered commands, and unintended access to other workspaces.
- Treat repository contents, LLM outputs, and diagram data as untrusted input. Restrict renderers and escape text.
- Evidence is not interpretation. Label `MEASURED`, `OBSERVED`, `INFERRED`, `UNKNOWN`, or equivalent provenance, and make missing evidence explicit.
- Prioritization changes attention, so reviewers must be able to inspect its explanation, sort differently, and access all changed files, including unassigned changes.
- When a renderer has insufficient data, show a useful unavailable state, not synthetic evidence.
- When Rex/Herdr or an agent is offline or not configured, topics, discussions, and decisions remain fully functional. Never expose Rex process-control operations to a read-only review client through an unguarded proxy.
- If a snapshot cannot be captured consistently, report it; do not mix versions without disclosure.

## 10. Implementation roadmap and scope

### Milestone M0 - Scaffold, fixtures, and a Rex integration spike

- `server/` Go module with migrations, tests, API schema, fixture generator.
- `client/macos/` SwiftUI app with API client; documented local/Tailscale setup.
- Fixture: workspace with **two distinct Git repositories**, one feature spanning both, 3+ topics, 2 intents, 3 observed changes, 2 findings, and linked evidence. Seed editable Text, Diagram, Code, Evidence representations.
- Establish `SessionBackend` and `AgentAdapter` as **separate interfaces** (stubs/fakes where semantic agent integration is not yet implemented).
- Implement initial `RexSessionBackend` discovery/inspection and lifecycle observation against a running Rex instance; show available runtime sessions and allow an explicit workspace association. Document the actual tested Rex API version and supported operations. No chat or Pi adapter required in M0.
- Add tests using a fake backend and one disconnected Rex fixture; confirm the core review service boots and works **without Rex installed or reachable**.
- Do not implement Herdr or Prunelle-managed sessions yet. Their future addition must not require changing `FeatureReview` or `ReviewTopic` schemas.

### Milestone M1 - Persistence and review capture

- Register workspace/repos, create feature review, capture multi-repo revision, serve immutable source diff/snapshots.
- Persist provider-neutral session/backend registration and optional workspace-to-runtime association, separately from review content. A disconnected provider is a recoverable integration failure, not a review error.
- Persist canonical topic graph, relationships, evidence, findings, comments, decisions.
- Serve event stream; restart/reconnect without data loss.
- Seed/edit topics and representations manually via API/fixture; **no LLM requirement**.

### Milestone M2 - Review UX vertical slice (the critical MVP)

- Topic tree and alternate Findings / All Source Changes tabs.
- Select topic, switch Text/Diagram/Code/Evidence, optionally display two views.
- Shared anchors/discussions across views; comment on topic and on code range.
- Engineering, Design, and Source perspectives; persistent preference.
- Trace view content to supporting source/test evidence; explicit unavailable states.
- Submit a review decision; revision-specific history.

### Milestone M3 - Revision-aware review

- Capture second revision; show added/changed/resolved topics and findings.
- Preserve human edits/discussions and original anchors; mark uncertain relocated code anchors outdated.
- Offer explicit unmatched/unassigned source changes and reviewer correction of topic links.

### Milestone M4 - Optional semantic analysis and first harness adapter

- One narrow analyzer that proposes topic grouping and/or an explainable attention signal; never overwrites manual edits.
- Add `PiAgentAdapter` separately from `RexSessionBackend`: discover/associate the *logical Pi conversation* with an existing Rex runtime, without scraping terminal output as the semantic protocol.
- Discuss a code/topic finding with that existing Pi session and correlate a reply to its thread.
- Submit structured `REQUEST_CHANGES`; implementation is deliberate and separate from discussion.
- Evaluate live versus forked session behavior before standardizing. Confirm the review remains usable when the Pi/Rex connection is gone.

### Milestone M5 - Future interoperability (not needed for prototype acceptance)

- Add `HerdrSessionBackend` using the same provider-neutral registry and capability model.
- Add `ManagedSessionBackend` to launch/supervise a supported agent via headless/RPC mode, with **no multiplexer prerequisite**; implement PTY support only if a harness requires it.
- Test the same review and agent bindings across multiple backend kinds without changing the review/topic UI or data model. Session migration/forking is not assumed.

### Out of scope for M0-M2

Automated semantic correctness guarantees, generalized multi-agent execution, semantic graph database, production metrics engine, auto-approval, real-time multi-user collaboration, GitHub/GitLab bidirectional sync, complex block/plugin marketplace, generative executable UI, building our own terminal multiplexer, Prunelle-managed process launching, harness durability, and comprehensive automated designer validation. Rex **integration** is in P0; building Rex functionality ourselves is not.

## 11. Acceptance criteria (executable behavior)

1. A workspace with repositories A and B appears as **one** feature review; code changes from both can be inspected without switching PRs.
2. Topic T links one intent, several observed changes, and a finding spanning both repositories. Those linked IDs stay the same while switching Text, Diagram, Code, and Evidence.
3. Clicking a diagram node or explanation evidence link highlights the corresponding code/evidence. An exact code-range comment can also be reached from the topic discussion.
4. A comment created while viewing Code remains visible when viewing Diagram or Text. Merely posting a comment never changes files or starts an implementation run.
5. Engineering/Design perspective switching reorders and selects views while keeping the same topics, findings, comments, and decisions.
6. Findings and All Source Changes are independently navigable. A changed file not linked to any topic appears under Unassigned Changes.
7. Reviewing an old revision shows its stored code/diff, even if the working tree changes later.
8. A second capture creates a new review revision; old comments/decisions persist and uncertain line mappings appear as outdated rather than silently moved.
9. The UI survives disconnect/reconnect and server restart with no missing persisted discussions.
10. Evidence provenance is visible and unavailable representations have an explicit reason. A generated interpretation cannot masquerade as a test result.
11. The review works without Pi, without an AI API key, and without any running coding agent.
12. API and domain tests cover multi-repo snapshots, many-to-many links, permission/path validation, comment creation, review decisions, and revision stability.
13. With a configured Rex instance, Prunelle can discover an existing session, inspect the provider-neutral runtime identity/status, and associate it explicitly with a workspace. This does not require scraping Pi terminal output or starting an agent.
14. With Rex disabled, unreachable, or not installed, Prunelle still starts, loads the full multi-repo review, accepts comments/decisions, and preserves state through restarts. Its session panel reports integration unavailability without breaking review.
15. A fake `SessionBackend` satisfies the same contract as the Rex implementation. Domain and API tests use Prunelle-owned IDs, not Rex-specific pane/session IDs, and do not require a live Rex server.
16. Agent sessions and runtime sessions remain distinct; any implementation of structured Pi reply correlation later must use explicit invocation/thread IDs, not terminal output timing. Adding Herdr or a managed backend must not require changing the topic/review schema.

Suggested test fixtures include a queue/worker example with an unverified retry finding and a distinct UI-flow topic. Verify no topic exists for at least one changed file to test exhaustive navigation.

## 12. Key architectural decisions and unresolved questions

**Decided for prototype:** Go central review server, SQLite, SwiftUI macOS client, Tailscale, workspace-first scope, Topic-first navigation, canonical intent/change/finding/evidence records, multiple view representations, shared discussions, exhaustive diff fallback, no agent dependency. **Use Rex as the first optional session backend**, with distinct `SessionBackend` and `AgentAdapter` interfaces and Prunelle-owned identities. No customer should be required to run a multiplexer; managed sessions are a future backend.

**Not yet decided:** Live or forked agent review conversations; automatic association of a logical harness conversation to a discovered runtime (initially explicit/manual); native versus embedded diff implementation; best analyzer and semantic-grouping strategy; collaboration/permissions; external PR synchronization; generalized generative UI. Implement seams, not speculative features.

**Important distinction:** Semantic topics are not JetBrains' independently applicable implementation chunks; a semantic topic may cut across many repos and need not be buildable in isolation. If technical chunks are later introduced, represent them separately with explicit dependencies.

**Success measure:** A senior engineer can understand and evaluate a cross-repository feature through intent, observed behavior, findings, and grounded representations, then drill to exact code when needed. Measure understanding, ability to detect seeded architectural/behavioral defects, attention allocation, and reviewer effort; don't optimize merely for prettier summaries.

## 13. Research basis

1. Casserini, Facchini & Ferrario (2026), *Beyond the 'Diff': Addressing Agentic Entropy in Agentic Software Development*: architectural conformity, observable agent process, and preserving system-level understanding. https://arxiv.org/abs/2604.16323
2. Gullstrand Heander et al. (2026), *Trust-Calibrated Code Review: A Participatory Design Study of Review Workflows for LLM-Generated Multi-File Changes* (JetBrains/Lund): trust calibration, overview/file/snippet review, seven proposed design constructs; 17 initial participants, 43 in follow-up survey. **63% expected lower overall review effort; this was not a measured productivity improvement.** https://doi.org/10.4230/LIPIcs.ESEM.2026.89
3. Bagirov et al. (2023), *Assessing the Impact of File Ordering Strategies on Code Review Process*: ordering affects reviewer attention, including evidence from IntelliJ IDEA reviews. https://arxiv.org/abs/2306.06956

Use these as research motivations, not proof of Prunelle's effectiveness. Validation requires user testing.
