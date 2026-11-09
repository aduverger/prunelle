# M2b — Automatic workspaces and context-aware CLI

**Status: implemented on `m2b-workspaces`; undergoing `/ship` simplification, validation and review. Not yet published.**

Issue: [#5](https://github.com/aduverger/prunelle/issues/5). M2b precedes M3. M3b was independently authorized and has merged in [PR #7](https://github.com/aduverger/prunelle/pull/7).

## Required outcome

From a supported workspace folder, start a review and capture without registering repositories or copying IDs. Use `workspace_root_paths` as workspace-parent directories. Discover only their immediate children; the configured roots are never workspaces. A child is either a Git repository or a folder containing immediate Git repositories. Preserve explicit IDs, immutable historical anchors, existing authentication/attribution and repository files.

## Workspace intent for review and publication

**User requirements and accepted decisions:** remove manual registration/repository enumeration and unnecessary ID copying from starting/capturing reviews. `workspace_root_paths` lists parent directories; discover only immediate children, never the configured roots themselves. Each candidate is one Git repository/worktree or its immediate member repositories. Infer creation from the actual invocation folder and infer capture only for the containing workspace's sole review; zero/multiple reviews must not select an unrelated review. Explicit IDs remain usable from elsewhere. Unused candidates stay out of ordinary navigation but can start their first review. Deletion hard-deletes workspace-owned Prunelle history/content, never project files, Git history or runtime processes. Preserve immutable historical anchors, attribution/human-only decisions, explicit independent retries and in-memory draft safety.

**Accepted scope and operational assumptions:** discovery/context are evaluated on the server filesystem; install/run the CLI from the user's actual project directory, without translating Mac paths or changing to the Prunelle source module. Creation and capture remain separate explicit operations. Retain the existing Go/Linux-first, pure-Go SQLite/single-writer, API-only loopback/Tailscale and React/MyGo architecture. Tests use disposable repositories/storage and disconnected/fake Rex, never user worktrees or live sessions. Native Mac launch/signing, live-tailnet streaming and capacity performance remain unverified. Rejected approaches are recursive depth-zero/one/two discovery, a separate namespace-exclusion setting, and the `workspace_search_paths` name. No archive, auto-fetch/branch switching, filesystem deletion, runtime control, persistent draft/outbox, automatic write retry, source editor or M3 comparison/re-anchoring is introduced.

**Agent choices, not additional user requirements:** optional titles default to the workspace name; existing base-ref/untracked defaults remain, with an initial override. Preserve manual/legacy explicit-ID forms and uniquely matching registrations. Use nullable unique folder identity and atomic registration/first-review creation; retain registered membership and reject changes instead of automatically reconciling them. Reject the obsolete configuration key with guidance. Keep administration in the CLI, preserve selected-review drafts after deletion, and scope completed receipts to support hard deletion without damaging unrelated retries. These choices are preserved for review, not silently promoted into the user's requirements.

**Review-disposition notes:** the membership-freeze/conflict policy is a potential usability mismatch with automatic discovery; the user did not request immutable workspace membership. Obsolete-key rejection is an agent-selected upgrade policy, not an explicitly requested compatibility rule. Hard deletion retains one deletion notification and body-less unknown-outcome claims; review these protocol exceptions against "all Prunelle-related stuff" rather than weakening retry safety or silently changing deletion behavior. The generated TypeScript creation type currently reduces the required-ID/path `oneOf` to `unknown | unknown`; server validation still enforces exclusivity, but static client enforcement is a review gap. None of these behaviors is removed or fixed in simplification.

## Approved product decisions

1. **Deletion/history — permanent application-data deletion.** Delete the selected workspace and its reviews, revisions, imported/semantic content, discussions, decisions and runtime associations in one database transaction. Never delete directories, Git history or working files; never stop runtime sessions. The folder remains discoverable, but discovery alone creates no registration. Deliberately starting another review there creates fresh application identities; it does not restore deleted history. Require an explicit workspace ID and destructive confirmation. The user explicitly approved hard deletion.
2. **One-level discovery, with explicit workspace-parent configuration.** The user rejected namespace exclusions and recursive discovery. Rename the configuration to `workspace_root_paths`; each entry is a parent containing workspaces, never itself a candidate. Only immediate children qualify. This supersedes #5's original depth-zero/one/two rule. A configured root is excluded even if it is an immediate child of another configured root.
3. **Subdirectories resolve to the containing discovered workspace.** A member repository and its descendants resolve to the same parent workspace, not a separately discovered nested workspace. Never infer an unrelated review or guess between duplicate legacy registrations. Explicit IDs still work from elsewhere.

## Baseline ownership traced before implementation

- `server/cmd/prunelle/client.go`: CLI constructs generated API requests. Creation required workspace ID/title; capture required review ID. `main.go` loads configuration but does not change cwd. Use `os.Getwd()` from the running executable, not a source/module directory or a guessed shell `PWD`.
- `server/internal/review/workspaces.go`: domain owner for registration, canonical repository roots, labels and configuration; `git.ResolveRoot` already validates canonical containment and recognizes worktrees.
- `server/internal/review/reviews.go`: creates reviews, lists by workspace, and captures registered repositories into immutable revisions. Preserve this capture pipeline and its content limits.
- `server/internal/httpapi/handler.go`, `server/api/openapi.yaml`: validated REST routes and generated Go contracts. Existing middleware owns authentication, attribution and explicit idempotency replay.
- `server/internal/store/migrations/`: workspaces had no workspace-folder identity. Most foreign keys do not cascade; review/revision and topic-parent relationships need deliberate deletion ordering. Shared content-addressed blobs must not be removed while referenced by another workspace.
- `server/internal/sessions/service.go`: consumes the registered workspace directory for suggestions/associations. Discovery must not create registrations or runtime associations merely by reading candidates.
- `client/src/App.tsx` and `hooks.ts`: normal navigation is driven by reviews; SSE already invalidates queries. The selected review previously fell back to the first list entry if it disappeared. Deletion must not make that fallback silently discard local drafts.

## Implementation approach

### 1. Bounded, read-only discovery

Implement discovery in the existing review domain, using Git's existing canonical-root validation rather than another Git parser.

- Enumerate only immediate child directories of each configured `workspace_root_paths` entry; inspect each candidate's immediate children for member repositories. Configured roots themselves are never candidates.
- A repository workspace contains that repository only. Otherwise include immediate child repository roots only, not arbitrary nested repositories or subdirectories that merely happen to lie inside a Git worktree.
- Support both `.git` directories and worktree `.git` files; reject bare repositories as today.
- Canonicalize paths and deduplicate overlapping configured roots. Stay within the configured boundary, avoid symlink traversal outside it and do not descend into repository internals, `.git`, or unrelated deep trees.
- Return deterministic candidate paths, names and repository membership. Surface actionable scan failures instead of silently reporting an inaccessible workspace as empty.
- Scan on demand. No watcher, background registrar, persistent discovery cache, tombstone or suppression mechanism.
- Offer discovery separately from registered workspace administration. Candidates with no reviews do not appear in ordinary client navigation.

### 2. Resolve and create through the existing review path

- Add server-owned path resolution and a read-only candidate API for CLI discovery/context. Keep discovery and cwd resolution on the server filesystem; a Mac client path is not silently translated into a different server path.
- Extend review creation to accept either an explicit workspace ID or a workspace path. Validate the alternatives rather than guessing when both or neither are supplied.
- Register a discovered workspace and create its first review atomically. Concurrent creation at one canonical folder must not produce duplicate automatic workspace registrations.
- Add only the workspace-folder identity needed for that guarantee. Preserve existing IDs/configuration and arbitrary manually registered repository sets.
- Reuse an existing registration when its canonical repository membership identifies it uniquely. If multiple legacy registrations match, report their IDs rather than select one or merge histories.
- Do not silently rewrite repository base refs, reinterpret historical repository IDs or retarget stored anchors. The registered repository set stays fixed; discovery reports a conflict if membership changes. An explicit ID retains the existing registered set rather than inventing an automatic reconciliation policy.
- Preserve `main` as the existing default base ref and untracked inclusion. Provide `--base-ref` for initial automatic registration, including `origin/main` for the user's remote-tracking setup; do not fetch, switch branches or mutate an existing workspace's settings implicitly.

### 3. CLI workflow

Commands, with flags before positional arguments as required by the existing Go flag parser:

```sh
# In a supported server-side workspace folder:
prunelle review create "Missing Permission"
prunelle review capture

# Optional title defaults to the workspace name, avoiding duplicate naming:
prunelle review create

# Initial base override, without manual registration:
prunelle review create --base-ref origin/main "Missing Permission"

# Explicit choices still work from outside the workspace:
prunelle review create --workspace WORKSPACE_ID "Review title"
prunelle review capture REVIEW_ID

# Read-only discovery and destructive application-data administration:
prunelle workspace ls --discover
prunelle workspace ls
prunelle workspace delete --yes WORKSPACE_ID
```

Keep the released `review create WORKSPACE_ID TITLE` and manual `workspace add` forms working. They are optional controls, not required setup.

For inferred capture, resolve cwd to its workspace and require exactly one associated review, counting all review statuses. Zero reviews reports how to create one; multiple reviews reports the available IDs/titles and requires explicit selection. Never fall back to a global latest review. Keep creation and capture as separate, explicit mutations; no automatic write retries.

### 4. Hard deletion

- Add `DELETE /v1/workspaces/{workspaceId}` and CLI confirmation/help. Authorization and attribution follow the existing workspace mutation path; do not introduce credentials or conflate attribution with authentication.
- Explicitly delete workspace-owned relational data in dependency order in one writer transaction; handle cyclic references without disabling integrity protections. Preserve other workspaces, shared blobs and runtime records. Remove past events for the deleted workspace/reviews and completed response receipts scoped to that workspace; keep unrelated event/retry state.
- Remove blobs only when no remaining content/anchor references them. Do not add a general-purpose cascade engine or maintenance worker.
- Append `workspace.deleted` in the same transaction and use existing post-commit notification. A failed deletion rolls back all changes.
- Repeating deletion cannot affect a newly created workspace at the same folder: deletion addresses an ID, not a rediscovered path. An absent ID returns 204, making explicit retry safe without keeping deletion tombstones.
- No filesystem deletion, Git writes, runtime termination or automatic resurrection.

### 5. Client integration

- Regenerate TypeScript from the changed OpenAPI contract; use existing transport/query hooks.
- Replace the empty-state instruction to manually register a workspace with the new CLI workflow. Normal navigation remains review-driven, excluding unused discovery candidates.
- On deletion of the selected review's workspace, retain the current in-memory draft context with an honest unavailable/deleted notice. Do not silently mount another review and lose text/retry state. Deliberate navigation still uses the existing leave-drafts guard.
- No new workspace browser, registration dialog, archive UI, persistent drafts or retry registry is required by this issue.

## Verification

Use disposable directories, a temporary database and explicitly identified real Git fixtures. Never scan the user's configured live roots in tests or modify user repositories/sessions.

1. Both required layouts under their configured workspace-parent directories, root exclusion, immediate-child-only discovery, worktrees, overlapping configured roots, symlink escapes, non-repositories and bare repositories.
2. Exact-folder and descendant resolution; no duplicate registrations from repeated or concurrent creation; legacy unique/ambiguous registration reuse.
3. Build and invoke the actual CLI from fixture folders outside the source/module checkout. Create without IDs, optional/default title, explicit ID overrides outside roots, and initial `origin/main` override.
4. Capture with zero/one/multiple reviews and unrelated reviews elsewhere; explicit selection remains valid. Existing capture failures must remain explicit and leave no partial revision.
5. Delete a workspace with multiple revisions, semantic imports, every discussion anchor family, messages, decisions and runtime associations. Verify foreign-key integrity, other workspace/shared content survival, transactional rollback, repeated deletion and deliberate recreation after rediscovery.
6. Compare repository files, Git refs/status and history before/after deletion; verify no runtime-control invocation.
7. Real-server browser deletion while a code/reply/decision draft is dirty: draft retained, missing context clearly reported, explicit navigation warns. Retain all existing browser and native smoke scenarios.
8. Standard server/client formatting, vet/lint/types, generation freshness, Go race tests, frontend/model/browser tests, production build, Linux MyGo smoke and Darwin arm64 cross-build. Validate browser/native fixtures with ambient Git identity disabled.

## Decision ledger — least confident first

1. **Do not automatically change existing repository membership.** The issue does not define adding/removing repositories from a registered workspace with historical revisions. Keep the registered set and fail inferred operations visibly if the directory's membership changes; explicit IDs retain the existing set. If asking were free, I would ask whether a separate membership-refresh operation is wanted. This milestone does not invent repository retirement/reconciliation state.
2. **Keep creation/deletion in the CLI.** The issue requires first-review creation from the workspace folder and supported deletion, not new desktop administration screens. Update empty/deleted client states and preserve drafts, but add no workspace browser or registration dialog.
3. **Default an omitted title to the registered workspace name.** This addresses the previously reported duplicate-naming friction. Preserve the existing `main`/include-untracked defaults and offer `--base-ref` on initial automatic registration; do not guess a remote/default branch. For an existing inferred workspace, an explicit base ref must match its stored settings.
4. **Reject the obsolete configuration key with migration guidance.** `allowed_roots` had different discovery meaning. A silent alias would accept the user's broad `~/code` configuration without defining the intended namespace boundary. Expand home paths for `workspace_root_paths` and require the user to choose actual parent directories. Existing database/history IDs remain intact; manual registration and released positional CLI forms still work explicitly.
5. **Use canonical folder identity, not names, for automatic registration.** Add one nullable unique workspace root path. Reuse an unclaimed legacy registration only when its exact canonical repository set matches uniquely; preserve its name, repository IDs and settings. Recheck under the existing single-writer transaction so concurrent review creation cannot duplicate the workspace.
6. **Hard delete owned content, including completed response receipts.** Cached mutation receipts can contain discussion text. A relational `resource_workspaces` view supplies one ownership rule for their migration/backfill and completion, with a workspace foreign key/cascade for cleanup. A late completion cannot cache deleted content. Body-less unknown-outcome claims retain the existing 24-hour protection; unrelated retries remain replayable. Delete past workspace/review events and retain only the new deletion notification, without resetting the global sequence.
7. **Delete by explicit ID, with `--yes`, and return 204 for an absent ID.** No interactive input or permanent tombstone is needed. A second deletion using an old ID cannot remove a fresh workspace at the same folder. Relational deletion is dependency-ordered with foreign keys enabled; remove blobs only when no remaining stored file/evidence/discussion references them. This is application-data deletion, not filesystem deletion or runtime control.
8. **Use physical server-side paths and revalidate capture roots.** Canonicalize configured root aliases, skip symlink directory entries, and resolve cwd to the closest containing supported candidate if roots overlap. Capture rechecks each registered repository's canonical containment, preventing a replaced/symlinked directory from escaping the configuration while old stored revisions remain readable. No shell `PWD` guess, host-path translation or Git fetch is introduced.
9. **Pin the selected review ID rather than fall back after deletion.** Keep its existing query data/controllers mounted on failed refresh so code, reply and decision drafts remain in memory. Render an unavailable notice and keep deliberate navigation behind the existing draft guard; no persistent draft store is added.

Hard deletion, immediate-child-only discovery and the `workspace_root_paths` name are confirmed user decisions, not guesses. Discovery is read-only; registration and first-review creation share one transaction, and discovery alone never resurrects deleted metadata.

## Verification results

- Server: formatting, `go generate ./...`, vet, full `go test -race -count=1 ./...` and Darwin arm64 cross-build passed. Domain/contract/CLI tests cover one/four-repository layouts, worktrees, root exclusion, depth and symlink boundaries, zero/one/multiple-review inference, legacy reuse/ambiguity, concurrent creation, creation/deletion rollback, immutable historical reads, hard deletion of all anchor families and preservation of other workspace/shared content. File contents (including untracked files), refs, status and commit history are compared across deletion.
- The actual built CLI was invoked from disposable project/member/source folders outside the Prunelle module. Optional/default titles, initial `origin/main`, explicit IDs from unrelated folders, legacy positional syntax, destructive confirmation, deletion, recreation and retry with the old ID passed.
- Storage/protocol regressions passed: a real pre-M2b schema with existing review/import receipts migrates to scoped ownership; deleted discussion text cannot be replayed, unrelated receipts still replay, and a deliberately late completion retains only a body-less unknown-outcome claim. Foreign keys remain enabled, deletion rollback preserves history, and runtime association deletion leaves the fake runtime online.
- Client: frozen installation/peer checks, formatting, ESLint, both model tests, typechecked production build, Go formatting/vet/race and Darwin arm64 cross-build passed. Repeating Go/TypeScript generation produced byte-identical generated files.
- **25/25 real-server browser scenarios passed**, retaining all 21 previous scenarios plus unused-candidate visibility/first creation and remote deletion with dirty code/reply/decision drafts. The three deletion cases verify preserved context/text, an honest failed post to the deleted resource, cancellation of navigation with the existing warning, and deliberate navigation afterward.
- Native Linux MyGo/WebKit smoke passed with bundled production UI, official Go-backed REST/SSE, actual Highlights, Pierre tree navigation, exact stored inline anchors and review decision UI. Browser and native suites ran with ambient Git author/committer variables unset, global/system configuration disabled and `user.useConfigOnly=true`; no OS identity inference masked fixture setup.
- Initial new-browser failures were test setup mistakes (a `<summary>` queried as a button and a response predicate missing Vite's `/api` prefix), corrected to the existing public summary interaction and actual transport URL. No application behavior or assertions were weakened. An initial new-domain setup used a nonexistent database directory; it now uses its own existing temporary directory.
- No test listeners remained on 17391/15173. Tests used temporary workspaces/storage and a disconnected/fake Rex backend, not user repositories or live sessions.

## `/ship` simplification pass

Reviewed the complete rebased branch against the intent above and the existing CLI → generated API → review domain → SQLite/event path, including the registered-workspace/session consumers, generated contracts and draft controllers. No repository-local or ancestor `AGENTS.md`/`CLAUDE.md` adds instructions to the existing workspace guidance. Only listed files were edited; tests and public contracts were left unchanged for the following Test/review phases.

### Simplification decisions — least confident first

1. **Use the fresh candidate's repository slice directly.** `resolveWorkspace` constructs a new candidate for each request and shares no discovery cache. Initial base-ref overrides only change that local candidate; registration matching uses canonical paths, not base-ref defaults. Remove the second slice representation rather than adding a preparation/registration abstraction. Git validation remains outside the write transaction and the match is still rechecked inside it.
2. **Have one zero-review error path.** Load reviews only when a matching registration exists, then select zero/one/many in the same domain function. An unregistered candidate still makes no global review query; existing error messages/details and explicit selection behavior stay intact.
3. **Normalize the title once and flatten the loader placeholder conditional.** Reuse the trimmed title and a named loading/unavailable message. This changes no defaulting, validation, rendering, selected-review identity or draft lifetime.
4. **Retain mechanisms with concrete safety requirements.** The nullable unique folder identity and shared prepare/insert path prevent duplicate registration while preserving released manual registrations. Receipt ownership/backfill/cascade and the late-completion guard prevent deleted text from reappearing through replay without clearing unrelated retries. Dependency-ordered deletion, shared-blob reference checks and root revalidation protect actual data/filesystem boundaries. Removing them would change observable guarantees, not merely simplify architecture. No watchers, generic cascade engine, extra UI or persistence framework was added.

### Simplification validation

- `cd server && go vet ./internal/review ./internal/httpapi ./internal/store ./internal/config ./cmd/prunelle && go test -race -count=1 ./internal/review ./internal/httpapi ./internal/store ./internal/config ./cmd/prunelle` — passed, including real CLI create/capture, concurrency, ambiguity, rollback/deletion/recreation and receipt scope/late-completion regressions.
- `cd client && pnpm format:check && pnpm lint && pnpm test && pnpm build` — passed; both model tests and the typechecked production build succeeded. Existing chunk-size warnings remain visible.
- Existing `tests/workspaces.spec.ts` under real Playwright/server/Vite with ambient Git identity unset, global/system config disabled and `user.useConfigOnly=true` — **4/4 passed**: unused-candidate creation and code/reply/decision draft retention, failed writes and guarded navigation after deletion.
- Go/TypeScript generation repeated with before/after SHA-256 checks — both generated files remained byte-identical. Changed Go files, plan formatting and `git diff --check` passed; only the three source files and this ledger changed, with no commit made and no test listeners left running.
- Broader browser/native/Mac/live-tailnet results above are prior implementation evidence, not newly rerun checks in this focused pass. Test curation and independent review remain next. The intent/disposition notes above explicitly preserve agent-added product choices rather than silently remove them.

### Remaining verification/release prerequisites

- Local changes are not published and have no new GitHub CI result yet. Update the server configuration from `allowed_roots` to the approved `workspace_root_paths` parents before deployment; an obsolete key intentionally fails with guidance. Normal startup applies migration 0004 to the existing database.
- Native macOS launch/signing/notarization and live-tailnet behavior were not exercised here. Existing bundle-size and ResizeObserver warnings remain visible; no performance/capacity claim or warning suppression was added.
- Before publication, reconcile issue #5's original multi-depth discovery text with the user's explicitly approved one-level rule recorded above. No remote issue, user configuration/database, repository files or live runtimes were changed by this task.
