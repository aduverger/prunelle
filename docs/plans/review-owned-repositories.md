# Review-owned repositories — model revision

**Status: implemented and locally validated; not deployed or published.** All recommended behavior and the filesystem trust model were explicitly approved. Follow-up to merged [PR #8](https://github.com/aduverger/prunelle/pull/8), commit `3842868`. This replaces the public workspace model, not merely its UI label. Finish the model decision before extending it for M3 revision-aware review.

## Intent and accepted direction

The review is Prunelle's top-level product object. It contains the repositories being reviewed, captured revisions, semantic explanations, discussions and decisions. A source folder helps infer command context; it is not another named object the user must register, select, search or delete.

Accepted from the discussion:

- No first-class workspace UI or separate workspace lifecycle. Show review titles and repository context, not duplicate `workspace / review` names.
- Repository information should support future filtering/search. That observation is not a request to build a search UI or global repository catalog now.
- Reviews started from the same folder are independent; their repository settings must not be coupled through a shared workspace.
- Base ref belongs to snapshot capture. Configuration supplies a default; a capture can override it. Resolve the default at capture time, not once during registration.
- Keep the existing merge-base semantics: each repository computes `merge-base(HEAD, requested_ref)`, and its snapshot stores the requested ref and resolved base/head SHAs. No automatic fetch or branch switching.
- The proposed relative `root_paths` values were agreed to resolve against the config file's directory, not the server's launch directory. Removing root configuration makes that specific rule unnecessary; do not silently broaden it into changes to existing `data_dir` or executable-path settings. CLI path arguments still resolve against actual invocation cwd.
- Preserve stored source, immutable original-revision anchors, review content/authorship, explicit independent retries and in-memory draft safety. Hard deletion never deletes project files/Git history or controls runtimes.

The earlier `[[workspace]] root_paths/default_base_ref` configuration proposal predates the decision to remove workspace management. The user subsequently approved its replacement and the authorization implications recorded below before implementation.

## Approved decisions

| Decision                                            | Accepted recommendation                                                                                                                                                                                             | Why confirmation is needed                                                                                                                                                                                                               |
| --------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Filesystem authority without a configured allowlist | Remove mandatory discovery roots and validate explicit source paths. Treat every authenticated caller as trusted to request any Git source readable by the server account.                                          | Removing the existing allowlist expands filesystem authority for `allowed_logins`, not just discovery convenience. Git validation is not authorization. Do not remove this guard before accepting that trust model.                      |
| Configuration after removing the workspace concept  | Use one `[snapshot]` table with `default_base_ref = "origin/main"`; no `root_paths` and no unkeyed array of workspace blocks.                                                                                       | The user preferred grouped `[[workspace]]` configuration earlier, but the corresponding object is now being retired. The replacement section name is a proposal.                                                                         |
| Default when no base ref is configured              | Preserve `main` as the built-in fallback; document `origin/main` explicitly in the normal config example. Never silently fall back to another branch when a requested ref is missing.                               | The requested config value is `origin/main`; an unconditional change to the built-in fallback was not separately agreed.                                                                                                                 |
| Existing stored base-ref preferences                | Audit/export existing per-repository refs before migration; future captures use the new configuration/explicit capture inputs, with no hidden legacy-default mechanism. Preserve all historical snapshot refs/SHAs. | Old per-repository settings may differ, and default vs explicitly chosen refs are not distinguished in the current schema. Approve their retirement or request a concrete continuing override requirement; do not discard them silently. |
| Existing runtime associations shared by reviews     | Map an old workspace association to every existing review that currently sees it; create no automatic associations for future reviews. Leave runtimes from zero-review workspaces running but unassociated.         | This preserves current visible associations, but the new association owner is a review. Confirm fan-out rather than invent an arbitrary recipient.                                                                                       |
| First review from an unregistered multi-repo member | Require the container folder as the creation target, or an explicit `--path`. Inside an otherwise unknown Git repository, create a single-repository review rather than guess its parent grouping.                  | A namespace and a multi-repo container cannot be distinguished from filesystem shape alone without roots/markers. Existing review context can help later commands, but cannot establish unknown first-use intent.                        |

The user explicitly accepted these recommendations after requesting implementation. The compatibility inventory identified the in-repository CLI, fixture runner, React client, session integrations, native/browser scripts and generated clients; they are upgraded together. No independently maintained external consumer was identified.

## Pre-change flow and concrete coupling (at `3842868`)

- `server/cmd/prunelle/client.go` creates a workspace first or supplies `workspace_id`/`workspace_path` to review creation. `--base-ref` currently configures registration, while capture accepts only a review ID. Fixture seeding also registers a workspace before creating its review.
- `review.Service.CreateReview`, `workspaces.go` and `discovery.go` bind multiple reviews to one workspace and reuse its repository rows/settings. A unique workspace `root_path` enforces one shared registration per folder.
- `CaptureRevision` loads repositories using `review.workspace_id`. It already stores the chosen base ref and resolved SHAs in `repo_snapshots`; reuse this capture pipeline rather than implement another snapshot engine.
- Migrations 0001–0004 attach repositories and reviews to workspaces. Repository IDs are referenced by snapshots, code evidence and code discussions. Multiple historical reviews can reference the same repository ID.
- `resource_workspaces` and `idempotency_keys.workspace_id` scope cached responses for deletion; `DeleteWorkspace` removes every associated review, its owned records, events and unreferenced blobs.
- `sessions.WorkspaceDirectory`, workspace/runtime associations and runtime DTOs expose workspace ownership. The desktop Integrations panel consumes those fields/routes.
- `App.tsx` separately queries workspaces to prefix review names and passes a workspace ID to Integrations. Its recently added unavailable-context/draft-retention behavior must survive the model change.

These are the producers/consumers to change together. Renaming only the UI or adding a parallel review registry would retain the underlying coupling.

## Target model

```text
Review
  id, title, description, status
  optional context_path                    CLI context, not workspace identity
  repository memberships                   review-owned scope/settings
  revisions → repository snapshots         immutable captured inputs/content
  topics / evidence / discussions / decisions
  explicit runtime associations
```

- `context_path` is an optional canonical server-side folder for cwd resolution. Multiple reviews may have the same path; it is not unique, an allowlist, or a separate lifecycle.
- A review's repository membership owns labels and current capture inclusion settings. Creating another review re-evaluates its selected source rather than inheriting mutable settings from an older review.
- Retain stable repository identity records for historical references. They are implementation identities, not a new user-managed project registry.
- Keep one authoritative review-repository relation for creation, capture, CLI lookup, source context, session suggestions and deletion. Do not reconstruct ownership independently in those consumers.
- No new automatic membership-refresh framework. A new review can include the folder's current repositories without modifying an existing review. Capturing an existing review uses its declared set and validates those paths; an unrelated newly added sibling repository does not itself block capture. Editing the membership of an existing review is outside this change unless explicitly requested.

### Preserving repository IDs without shared settings

**Do not clone/reassign old repository IDs to make a convenient one-review foreign key.** That would rewrite IDs embedded in immutable snapshot/evidence/discussion anchors and invalidate existing source links.

Implemented relational shape:

- `repositories`: stable existing `id` and canonical source identity/path.
- `review_repositories`: `(review_id, repository_id)`, review-local label and inclusion settings; unique labels/paths within a review, not across all reviews.
- Populate each membership from the repository set its review currently uses. Two migrated reviews may retain the same historical repository identity while having separate settings/lifecycles.
- Keep snapshot repository IDs, revision IDs/ordinals/fingerprints, evidence ranges, selected text, blob hashes, discussion IDs/messages and decision references unchanged.
- Historical reads must obtain labels/source metadata through a well-defined owning review relation. Do not add speculative label-history fields or a remote-URL identity/deduplication system.
- Do not merge existing repository identities merely because their paths/labels match. Cross-worktree project grouping is not required to remove workspace management.

The association is justified by actual existing shared references, not hypothetical future providers. New source identities can remain private implementation records; no repository-registration command or UI is introduced.

## User workflow and contract

Implemented syntax:

```sh
# First creation targets the repository or multi-repo container:
cd ~/code/emidat/unblock_elems
prunelle review create "Missing Permission"

# Explicit source target from elsewhere; no workspace registration:
prunelle review create --path ~/code/emidat/unblock_elems "Missing Permission"

# Capture defaults are evaluated now, not when the review was created:
prunelle review capture
prunelle review capture --base-ref origin/main REVIEW_ID

# Explicit review operations remain valid from any folder:
prunelle review capture REVIEW_ID
prunelle review import REVIEW_ID manifest.json
prunelle review delete --yes REVIEW_ID
```

Rules:

1. Creation always creates an independent review. An omitted title can continue to default to the target folder/repository name; it does not introduce a second name.
2. Resolve source on demand. A Git repository/worktree contributes itself; an explicitly targeted non-Git container contributes immediate child Git repositories/worktrees only. Validate canonical paths, reject bare/broken repositories, avoid following arbitrary symlink directory entries and never recursively sweep a namespace.
3. If existing review context identifies one containing source folder, it can identify the creation target without copying another review's membership/settings. Otherwise use the explicit target or containing Git root; do not guess an unknown parent grouping.
4. Inferred capture resolves against persisted review context and member repository containment, then requires exactly one matching review, including all statuses. Zero/multiple matches give a useful error/list of IDs; no latest/global/nearest-review guess. An explicit ID bypasses cwd selection, not validation or authentication.
5. Preserve explicit arbitrary repository-set creation currently possible through manual workspace registration. Accept an inline repository list on review creation rather than requiring a reusable workspace. Decide the minimal repeated CLI flag spelling during implementation; do not add a project registry for it.
6. Review deletion is confirmed, ID-addressed, hard deletion. Repeating an absent ID returns success and cannot delete a newly created review at the same path.

### API changes

- Review responses carry repository context directly; clients should not fetch a workspace to display or operate on a review.
- Review creation accepts an explicit server-side source path or explicit repository set, not a workspace ID. Keep these alternatives typed and exclusive in OpenAPI/Go/TypeScript; fix the current `unknown | unknown` generated-type gap rather than relying on runtime validation alone.
- Capture accepts optional capture inputs; an empty request remains valid. Preserve per-repository comparison capability through explicit capture inputs if mixed refs are needed, rather than persisting them on a shared workspace. Validate overrides against that review's members and reject unknown/duplicate targets.
- Add `DELETE /v1/reviews/{reviewId}`; adapt existing deletion mechanics, not a second cleanup subsystem.
- Move runtime association/filtering/DTO fields to review IDs and reuse the same explicit association UI and idempotent PUT/DELETE semantics.
- Retire workspace discovery/CRUD endpoints, workspace query parameters and workspace CLI administration. No public replacement object called “source workspace”.
- Review IDs and history remain valid. Use a coordinated server/CLI/desktop upgrade with an explicit breaking-change note; do not maintain dual workspace/review APIs indefinitely. If concrete external consumers require a transition, agree its exact scope before adding adapters.

## Capture defaults and configuration

Accepted configuration:

```toml
[snapshot]
default_base_ref = "origin/main"
```

- Resolve per-repository override, then capture-wide override, then server snapshot default, then the approved built-in fallback.
- Freeze the resolved input for that capture attempt. Configuration changes affect future captures, never stored snapshots or a retry of an already committed idempotent capture.
- Store the actual requested ref and merge-base/head SHAs per repository snapshot, as today. A missing ref fails explicitly and leaves no partial revision. Do not fetch, change branches or guess another default.
- Remove creation-time `--base-ref` and mutable repository-level base defaults once the legacy-preference decision is settled.
- Default configuration is server-authoritative. The desktop does not independently guess a branch or duplicate capture rules; surface the resolved ref in existing revision/source context.
- Remove discovery-root configuration only after accepting the filesystem trust change. Authentication/attribution, human-only decisions, loopback/Tailscale transport and no-CORS-exception behavior are unchanged. Validate Git/path inputs even with no allowlist, but do not describe validation as a filesystem permission barrier.

## Migration and deletion safety

This follows a merged, deployed-capable model. Do not edit migrations 0001–0004 or reset the database.

1. Build a disposable version-4 migration fixture with: one workspace shared by several reviews, unrelated workspaces, all anchor families, multiple revisions, shared blobs, completed/pending receipts and runtime associations. Record public historical reads/IDs before migration.
2. Add review membership/context/receipt ownership and migrate every existing review to its current repository set. Keep existing review titles; no duplicate workspace name is required in the UI. Preserve all original repository references rather than rewriting history to fit the new ownership shape.
3. Copy an existing workspace `root_path` into each associated review's context. Do not infer an unrelated common ancestor for arbitrary legacy repository sets with no recorded context. Those reviews remain addressable by ID/member-repository context; report missing container context in upgrade guidance instead of adding a guessy repair framework.
4. Migrate workspace runtime links according to the agreed policy. Keep runtime identities/states; never start, stop or fabricate agent activity/author events during migration.
5. Replace receipt ownership with review ownership using the existing canonical resource relationships. Migrate identifiable review/revision/import/topic/discussion/decision receipts. Deliberately retire receipts for removed workspace-only operations; do not blanket-clear unrelated completed receipts or body-less unknown-outcome protection. Verify late completion cannot restore deleted review text.
6. Replace workspace deletion with review-scoped dependency-ordered cleanup. Deleting review A must preserve review B even when they share a context path, old repository IDs, blobs or runtimes. Remove a source identity/blob only when no remaining membership, snapshot or anchor references it. Keep the monotonic event log and durable review-deletion notification; remove deleted review history without purging unrelated events.
7. Remove obsolete workspace columns/tables, indices/views and domain interfaces once the migrated paths use the new owner. Prove SQLite table-rebuild/FK behavior on the fixture before finalizing DDL. Do not disable integrity checks on a live database, silently rewrite anchor IDs or retain two active ownership models to avoid the migration.
8. Make the upgrade transactional with rollback on failure and foreign-key checks. Back up the data directory with the server stopped before rollout; old binaries cannot reopen a newer schema safely. Upgrade the embedded desktop/CLI contract in the same rollout.

Exact DDL depends on the migration proof, not on speculative generic migration infrastructure. If preserving IDs exposes a broader format/API incompatibility, stop and resolve it before implementing a lossy shortcut.

## Implementation sequence and affected owners

1. **Confirm the decisions above.** Record the accepted security boundary, configuration/default policy, legacy preference/association handling and first-use inference. Inventory real callers of the merged workspace API/CLI.
2. **Prove storage migration first.** `store/migrations`, stable repository references, review memberships, receipt ownership and cross-review deletion isolation. No production database mutation for the proof.
3. **Switch the domain/CLI/API together.** `review/{types,reviews,workspaces,discovery,delete_workspace}.go`, `git/root.go`, config, handlers/OpenAPI/generated clients, CLI/fixture seeding. Keep the existing Git capture/immutable content path; remove the parallel registration/discovery lifecycle.
4. **Switch sessions and desktop consumers.** Review-scoped directory/associations; `App.tsx`, `Sessions.tsx`, query keys and generated TS. Remove workspace-name prefixes/queries; expose repository context through existing source/revision/integration components. Preserve dirty drafts and unavailable-review state after deletion.
5. **Document and validate the coordinated upgrade.** Update README, delivery plan and architectural decisions; rewrite fixtures around review creation, migrate existing databases, replace retired workspace contracts with the new review contracts, retain existing semantic/inline/native regressions.

Prefer one focused follow-up PR, with migration and consumers kept coherent, rather than a public interim dual model. Implementation is authorized by the subsequent approval; publication remains a separate workflow.

## Acceptance and verification

- From a repository or explicit multi-repo folder, create and capture without configured discovery roots or workspace IDs, subject to the accepted authorization policy. Reject namespace guessing, malformed paths, unsupported Git roots and ambiguous review context.
- Two reviews created from the same folder have independent memberships/settings. Creating a later review after adding a repository does not mutate or block an older review merely because the folder changed.
- Configuration changes and explicit capture ref overrides affect only future captures. Missing refs fail atomically; committed idempotent capture retries return the original revision despite later config changes. Test mixed refs if that capability is retained.
- Migrate version-4 data without changing any review/repository/revision/anchor/message/decision IDs or stored source/ref/SHA/fingerprint values. Compare actual public reads, not only table counts. Test rollback and the shared-repository-ID case explicitly.
- Hard-delete one migrated review while preserving the other review's full semantic content, source, receipts and runtime association. Prove unrelated replay and source-identity/blob reference protection, late completion safety and durable SSE resume across deletion/restart.
- Real CLI tests run from folders outside the source checkout. Cover explicit paths, default titles, zero/one/multiple reviews, arbitrary inline repository sets and old-command errors/upgrade guidance.
- Desktop navigation shows review titles/repository context without workspace queries or controls. Review-specific Integrations remains explicit; code/reply/decision drafts survive remote deletion until deliberate navigation. No editing one review may retarget another's source.
- Run standard server/client formatting, generation freshness and typed contract checks, vet/lint/race/model tests, real-server browser suite, bundled Linux MyGo/WebKit smoke and Darwin arm64 cross-builds. Keep fixtures temporary, Git identity explicit and Rex disconnected/fake. Native Mac launch/live-tailnet/capacity claims require separate actual verification.

## Implementation decision ledger — least confident first

1. **Keep retired refs as owned migration-audit metadata instead of a JSON sidecar.** The approved behavior requires auditing old preferences without using them as future defaults. An automatically retained sidecar would violate complete review deletion or require cross-filesystem/database cleanup machinery. Migration 0005 instead renames/copies the existing preference into nullable `repositories.legacy_base_ref`; new sources leave it null, capture never reads it, and normal source-identity deletion purges it. The stopped-server backup preserves the original schema too. If asking were free: would you prefer an explicit operator export command rather than this retained migration-only value?
2. **Keep cwd inference server-side behind `infer_context`.** An implicit CLI cwd may reuse one known containing context; an explicit `--path` must not silently select a broader old context. A boolean on path-based creation distinguishes these inputs without a second resolver/registry. Ambiguous containing contexts require explicit selection. If asking were free: should this inference exist only in the CLI? The server currently owns the authoritative containment rules.
3. **Use repeatable `--repo LABEL=PATH` and `--repo-base-ref REPOSITORY_ID=REF`.** This preserves arbitrary sets and mixed comparisons through existing commands, with ownership/duplicate validation; no new settings lifecycle. Inline-set titles default to joined labels. If asking were free: is this flag spelling preferable to a repository-set file?
4. **Give new reviews fresh repository identities; retain shared IDs only for migrated history.** This is the smallest implementation of independent mutable scope without path deduplication or anchor rewrites. No URL/project identity or search abstraction is added. If asking were free: should later repository filtering group worktrees/paths? That feature remains out of scope.
5. **Use a migration-specific offline SQLite rebuild.** Foreign keys are disabled on the sole startup writer before BEGIN, the complete resulting graph is checked before commit, and enforcement is restored/verified before serving. Do not disable checks in ordinary writes. Failed graph validation rolls back and can be retried. If asking were free: would rollout need a separate explicit migration command? Existing startup migration behavior is retained.
6. **Load defaults on server startup, resolve them for every capture.** Editing config requires restart, consistent with current config loading; no watcher/reload service is introduced. Captured refs/SHAs and committed retries remain immutable. If asking were free: was hot reload expected? Nothing in the current request requires it.
7. **Preserve repository-only runtime suggestions.** CLI context may match its container, but observational runtime suggestions still require cwd inside a member repository, as before. Both predicates live on the review domain; changing ownership does not broaden unrelated suggestion behavior.
8. **Reuse existing consumers and draft lifetime.** Review titles replace workspace prefixes, existing source summaries show the chosen ref, and Integrations uses review IDs. There is no new creation wizard, repository search, membership editor or draft persistence. New source validation does not reinterpret a broken Git root as a container.

## Implementation evidence and verification

- Disposable schema-v4 proofs preserve shared repository IDs, snapshots, code blobs/ranges, messages, imports, full semantic records, all eight discussion anchor families and decisions. Public reads remain valid; deleting one migrated review preserves the other source and retained legacy preference until its last owner is deleted.
- A deliberately invalid migration graph rolls back, restores FK enforcement and upgrades successfully on retry. Existing completed review receipts and body-less pending claims survive; retired workspace receipts do not.
- Regression coverage includes independent/re-scanned memberships, explicit inline sources, mixed capture refs, missing-ref rollback/recovery, idempotent capture replay after restart/default change, review-scoped runtime unlinking without stopping, late receipt completion safety, durable SSE resume after deletion/restart and in-memory desktop drafts after remote deletion.
- Cwd/CLI tests use actual temporary project directories outside the source checkout. Authenticated allowed logins can select readable Git sources outside any former root; unapproved logins remain forbidden.
- A bounded-reader test first reproduced a new nested-query deadlock (`context deadline exceeded`), then passed after list rows were released before loading membership. This is a correctness fix, not a performance abstraction.
- Generated TS creation inputs are concrete/exclusive rather than `unknown | unknown`; negative compile fixtures reject missing or simultaneous source alternatives.
- Full validation results are recorded below. No production configuration/database/source worktree or live Rex runtime is modified by these checks.

### Final local verification

| Check                                                        | Result                                                                                                             |
| ------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------ |
| Server/client Go formatting, vet and race suites             | Passed; final containment change also rechecked review/sessions/HTTP packages under race                           |
| Go API / TypeScript generation                               | Passed, byte-identical to checked files                                                                            |
| Frozen dependency install and peer checks                    | Passed                                                                                                             |
| Prettier, ESLint, TypeScript including negative API fixtures | Passed                                                                                                             |
| Client model tests / production bundle                       | 2 passed / passed                                                                                                  |
| Real-server Playwright suite                                 | 25 passed, hermetic Git identity/config (1.7m)                                                                     |
| Bundled Linux MyGo/WebKit REST/SSE/native smoke              | Passed, disposable storage/repos and no live Rex connection                                                        |
| Server and desktop Darwin arm64 cross-builds                 | Passed (unsigned; not a native launch)                                                                             |
| Branch/scope and cleanup                                     | `review-owned-model-plan`; migrations 0001–0004 unchanged; clean diff whitespace; no listeners left on 17391/15173 |

Detailed local logs: `/tmp/prunelle-review-owned-final-{server,client,browser,native,scope}.log`. Reader regression reproduction: `/tmp/prunelle-review-owned-reader-red.log`. Existing large-bundle warning remains; no threshold/suppression changes were made.

**Release prerequisites not performed:** stopped-server backup and migration of the user's deployed database, coordinated server/CLI/desktop installation, native macOS launch/signing/notarization, live-tailnet testing and maximum-capacity qualification. Historical-read migration tests use disposable version-4 SQLite fixtures, not production data. No production deployment or publication is claimed.

### Historical planning validation

The initial draft was documentation-only, based on main at `3842868`; it made no implementation-test claims. Implementation began only after the user accepted the trust/default decisions.

## Ship simplification — reviewer/PR intent

**User requirements and accepted decisions:** reviews are the sole product/lifecycle object and own independent source membership, history and runtime links. Capture chooses refs (per-repository override → capture-wide override → `[snapshot]` default → built-in `main`) with existing merge-base semantics, immutable requested refs/SHAs/source and unchanged committed retry results. Unknown first-use multi-repo context needs an explicit container; later cwd inference must be unambiguous, including closed reviews. All authenticated callers, including allowed logins, are explicitly trusted with any server-readable Git source; Git validation is not authorization. Preserve historical IDs/anchors, attribution, human-only decisions, SSE/ETag/retry guarantees and in-memory draft retention after remote deletion. Hard deletion removes owned Prunelle content, never source files/Git history or running processes, and preserves other owners of shared identities/blobs.

**Operational assumptions/non-goals:** retain Go/Linux-first pure-Go SQLite (WAL, one writer), React/MyGo/Pierre and loopback API behind Tailscale Serve; no new credentials, server UI or CORS exception. CLI paths use invocation cwd; unrelated config path semantics do not change. Config edits require server restart, not a hot-reload worker. Stop/back up the server and upgrade server/CLI/desktop together; old binaries reject the newer schema. Do not restore workspace registration/root lists, recursive namespace guessing, global/latest selection or rewritten historical repository IDs. No fetch/branch switching, automatic membership refresh, repository catalog/search UI, archive/editor, runtime/conversation control, persistent drafts/outbox or automatic write retries.

**Agent mechanisms, not extra product requirements:** `review_repositories` preserves actual shared historical identities; fresh reviews receive fresh source IDs. `context_path`/`infer_context`, repeatable CLI flag spelling, repository-only session suggestions and the startup-only FK rebuild are implementation choices recorded in the ledger above. Existing runtime links fan out only to existing reviews, never future ones.

**Review disposition item retained, not silently removed:** the approved legacy-preference audit/export recommendation was implemented as owned `legacy_base_ref` metadata, not an emitted export or audit command. Capture never reads it and last-owner deletion purges it. This was an agent-selected alternative to an unpurged JSON sidecar; the reviewer should confirm it meets the approved audit intent. Simplification preserves it rather than discarding values or inventing another export workflow.

### Simplification ledger — least confident first

1. Define each migrated receipt's owner once in a CTE, then use that same value for copying/filtering. Existing completed/global/pending retention and retired-workspace removal stay unchanged; this is not a cache or a changed retry rule.
2. Bind the immutable revision owner already read by `GetRevision` when resolving membership labels, instead of joining `revisions` a second time. No label-history field or alternate owner lookup is introduced.
3. Remove the former multi-review `owned_reviews` wrapper from single-ID deletion. Keep dependency-ordered cleanup, explicit link removal, reference-protected blob/identity cleanup, rollback and the durable deletion event.
4. Omit the eagerly allocated empty repository slice during preparation: successful creation already requires and appends at least one repository, so the API's successful array representation is unchanged.

No new product decisions were needed. No public API or test assertions changed; test curation remains the next phase.

### Simplification validation

- Passed Go formatting, focused vet and race suites for `review`, `store`, `httpapi`, `sessions` and `cmd/prunelle`, including version-4 migration/rollback/retry, original-revision reads, hard deletion/isolation, late receipts and SSE resume.
- Passed byte-identical Go/TS regeneration, client type checking (including exclusive-source negative fixtures) and both model tests.
- Passed the real-server review-lifecycle browser suite with hermetic Git identity/config: remote deletion retains code/reply/decision drafts, deliberate navigation warns, and source folders do not register themselves.
- Full browser/native/package/Darwin suites were not repeated in this focused phase; previous implementation results above remain historical. No production data or live Rex process was used. Logs: `/tmp/prunelle-review-owned-simplification-{go,contracts,browser}.log`.
