# M3b — Pierre-native code review and inline discussions

**Status: implemented locally on `m3b-client`; not yet published or merged.**

Ticket: [M3b #6](https://github.com/aduverger/prunelle/issues/6). M2b [#5](https://github.com/aduverger/prunelle/issues/5) remains the workspace/CLI milestone required before M3. This plan uses the requested M3b name; it does not rename M2b or add a new ordering dependency between M3 and M3b.

## Workspace intent for ship review

**User requirements:** make source review feel like the supplied DiffsHub reference using Pierre CodeView, native gutter utilities, line annotations and measured header/footer slots. Show composers and durable conversations beneath exact BASE/HEAD single/multiline ranges; keep code context stable through selection, typing, posting, replies, status changes and SSE. Add left Files/Comments navigation, remove the permanent right review column, launch decisions through **Review decision**, and make sessions secondary. Actually enable Highlights where supported.

**Preserved contracts and operations:** topics/findings and Text/Diagram/Code/Evidence remain available; exhaustive/unassigned and deleted source remain navigable without fake paths/content. The server owns immutable source, anchors, authorship, messages, decisions and membership. Old comments open their original revision, never an inferred current line. Freeze edited drafts, retain independent reply/status retry identities through virtualization, honor stale If-Match handling and explicit retries, keep Markdown safe and decisions human-only. Drafts remain in memory with navigation/reload warnings; only preferences persist. Keep the existing MyGo Go-backed REST/SSE transport, API-only loopback/Tailscale server, separate authenticated identity/agent attribution, and optional Rex metadata/explicit associations. Tests use disposable repositories and a disconnected fake Rex backend, never user worktrees/sessions.

**Agent-chosen mechanisms, not independent user requirements:** controlled CodeView items with revision/repository/path IDs; one continuous source canvas while semantic modes retain two views; split/light defaults and the numeric keyboard range form; native decision modal and Settings → Integrations; historical code scope without moving the global revision picker; untouched COMMENT forms follow the picker while dirty forms stay frozen. Review-lifetime controllers/portal hosts preserve existing mutation hooks; grouped annotations avoid duplicate location slots. These observable choices are retained during simplification, not silently removed.

**Non-goals/rejected approaches:** no server/API/schema changes, re-anchoring/comparison (M3), automatic workspace/CLI work (M2b), code editing, agent chat/process control, batch pending-review comments, persisted draft/outbox/retry framework, custom diff/token renderer or scroll/measurement system, speculative workers/provider/theme abstractions, fabricated authors or DiffsHub demo deletion/storage semantics. Keep the pinned Pierre/MyGo integrations; permit WASM narrowly, not JavaScript `unsafe-eval`. M2b remains required before M3, not an inferred blocker for the explicitly requested M3b work.

**Evidence limits:** the original macOS selection jump was not independently reproduced. Existing geometry/browser/native checks validate the replacement flow; Chrome ResizeObserver warnings, native macOS launch/signing and live tailnet verification remain the documented gaps below.

## Goal and required behavior

Make source review feel close to DiffsHub: a file/comment navigator on the left, a generous code viewport, good highlighting, a gutter `+`, and composers and saved threads directly below their code. Remove the permanent Discussions / Decisions / Sessions column. Preserve Prunelle's feature/topic/evidence model and existing review guarantees rather than replacing the product with a GitHub PR viewer.

- Hovering the line-number gutter reveals `+`; keyboard users have an equivalent labelled action. Single-line and multiline comments work on either BASE or HEAD in split and unified layouts.
- Selecting lines does not return to the file top. Opening/typing/posting a comment, replying, resolving and receiving SSE updates retain the relevant code context.
- The composer appears beneath the selected range; persisted threads remain beneath their anchored lines after reload. Comments in the left navigator open and highlight the exact context.
- Files and Comments/Discussions share the left navigation area. Decisions open on demand from **Review decision**. Optional runtime/session management moves to secondary settings/integration access.
- Pierre Highlights is actually selected on supported runtimes, not merely installed transitively.

## Sources inspected

- [Live reference: bun #30412, `.buildkite/ci.mjs`, BASE line 376](https://diffshub.com/oven-sh/bun/pull/30412#target=.buildkite/ci.mjs&start=D376). Inspected the running split-diff layout, left Files/Comments toggle, gutter `+`, selected range and composer inserted under the BASE line. No comment was submitted to the reference.
- [CodeView](https://diffs.com/docs#codeview), [annotation types](https://diffs.com/docs#core-types-lineannotation-and-difflineannotation), [header/footer](https://diffs.com/docs#codeview-header-footer), and [Highlights](https://diffs.com/highlights), plus the highlighter and React integration documentation.
- Upstream Pierre checkout `b6cf40c`: `apps/diffshub/components/{DiffsHubViewer,DraftAnnotation,ExampleAnnotation,ThemedCodeView}.tsx`, highlighter selection/theme code and the Diffs/Highlights skills and recipes. These show integration patterns, not reusable Prunelle business rules.
- Current Prunelle main `f01aea0`; installed declarations/source for `@pierre/diffs` 1.6.4. The lock already includes `@pierre/highlights` 0.4.0. The required CodeView, annotation, gutter, header/footer and highlighter APIs exist in the pinned version; no upgrade is justified merely to use them.

DiffsHub's demo uses transient comments and random personas. Reuse its layout and supported Pierre APIs, **not** its author, storage or comment-deletion semantics. Prunelle messages, actors, immutable snapshots and decisions remain server-owned.

## Existing end-to-end flow and owners

| Owner             | Current responsibility                                                                                                                                          | Planned extension                                                                                                                     |
| ----------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------- |
| `App.tsx`         | Review/revision selection, canonical `FileSelection`, evidence/thread navigation, representations, permanent right panel.                                       | Keep selection/orchestration here; replace the right column with left comment navigation and on-demand decision/integration surfaces. |
| `Views.tsx`       | Stored-file query, BASE/HEAD projection, `MultiFileDiff`, selected-line callback and detached “Discuss selected lines” action; text/diagram/evidence rendering. | Move code-specific mechanics into a coherent `CodeReview.tsx` surface; keep semantic rendering and grounding in their existing path.  |
| `SourceTree.tsx`  | Pierre Trees, exact repository/path selection, exhaustive/unassigned files and separate deleted-file trees.                                                     | Keep canonical membership and repository isolation; navigation targets CodeView items instead of replacing the code viewport.         |
| `Discussions.tsx` | Frozen new-discussion target/body, thread/reply/status actions, decisions and history.                                                                          | Separate discussion state/actions from placement, then render those same forms/threads through annotation slots or non-code context.  |
| `hooks.ts`        | Authoritative queries, SSE invalidation/reconnect and explicit retry/idempotency behavior.                                                                      | Reuse these guarantees; do not introduce a second transport, automatic write retry or outbox.                                         |
| `Sessions.tsx`    | Optional runtime metadata, explicit workspace associations, five-second polling while mounted.                                                                  | Reuse it from secondary integrations access; keep it independent of review loading and stop polling when closed.                      |

The current file renderer creates new options and selection/render callbacks during parent renders. Pierre's installed React adapter compares options and can force a render when callbacks change. This is a concrete path to investigate for the scroll jump, **not a root cause confirmed by a reproduction yet**. Start with a failing long-file regression before claiming the defect fixed.

## Proposed implementation

### 1. Give CodeView ownership of the code viewport

- Use React `CodeView` with **controlled `items`**, since authoritative files/discussions already belong to React/TanStack Query. Do not copy DiffsHub's imperative streaming machinery or mix `items` with `initialItems`/imperative item mutations.
- In source mode, present the revision's source changes as a continuous code list. Preserve topic/representation/evidence scoping when entering Code from semantic views; do not reinterpret source membership or bypass a representation's declared availability. Existing explicit source fallback remains available.
- Build `FileDiffMetadata` with `parseDiffFromFile` from existing stored-file responses. Reuse the query cache; never read the live working tree in the client or add a parallel server diff format.
- Use stable item IDs derived from revision + repository + path, not filenames alone. Memoize files, parsed metadata, items, options and render callbacks. Increment an item's presentation `version` only when its rendered source, annotations or collapsed state changes; selection/typing must not rebuild its file or remount the viewer.
- Use controlled `{ id, range }` selection. Use `scrollTo` item/line/range targets only for deliberate navigation or a minimal reveal of an off-screen composer, not as a post-render scroll-restoration loop. Keep one vertical scroll owner per code surface; CodeView owns its layout, virtualization and sticky file headers.
- Keep additions/deletions/renames and BASE/HEAD identity intact. `null` means a genuinely absent file side, not an empty file. Binary/omitted/unavailable files keep explicit metadata/notices and remain navigable; do not fabricate empty `FileContents` just to insert them into CodeView.
- Keep Pierre Trees and its deleted-file separation, including file/directory replacements and identical paths across repositories. Do not manufacture path aliases to combine repository labels.

### 2. Use CodeView's header/footer and native chrome

- Put source-scope/snapshot context and loading/error/empty/unavailable notices inside `renderCodeViewHeader`; use `renderCodeViewFooter` for end-of-scope context. Keep the global review toolbar, including **Review decision**, outside the scrolling code body.
- These regions are in-flow, non-virtualized and work with no items. Let CodeView measure them and track later size changes; do not add fixed height guesses, custom spacers or a competing ResizeObserver.
- Preserve native file headers, filename/rename presentation and diff statistics. Add Prunelle's repository/status identity through the existing header slots, not replacement diff headers.
- Use library `layout` for code-list padding/gaps. Match the reference's readable code scale, line height, gutter affordance, contrast and inline-card spacing; remove M2's hard-coded 10px diff font where it conflicts with that baseline. Prefer supported theme/CSS variables for adjacent comment cards; no shadow-DOM mutation or custom hunk/row renderer.

### 3. Put comment creation and persisted threads on annotations

- Enable Pierre's gutter utility and route its range/item callback to Prunelle's existing discussion target. If a labelled keyboard control needs custom content, use the supported gutter slot, not custom hit-testing/positioning.
- Project code discussions and the active composer into `DiffLineAnnotation` values. Map BASE to `deletions`, HEAD to `additions`; coordinates are one-based file-side lines, never visual rows or hunk offsets.
- Place a multiline annotation after the range's final line and retain the **full original range** for highlighting, labels and submission. Metadata carries stable draft/discussion IDs; line numbers are not application identity. Multiple threads on one location must all remain distinguishable.
- Render through `renderAnnotation(annotation, displayedFileOrDiff, item)`. During asynchronous rendering, verify the displayed content matches the intended snapshot before offering a write; the callback's item metadata can lead its displayed file.
- Keep one-sided range validation. Do not coerce a cross-side selection into a valid-looking anchor. Keep the five-field `CodeRangeInput` projection; never send response-only blob hashes/selected text back in requests.
- Reuse durable discussion/message/resolve/reopen endpoints, attribution and safe Markdown. Saved threads, replies and status controls render inline. No new comment storage, fabricated authors, editing/deletion of immutable messages, GitHub synchronization or “pending review comments” batch API.
- A thread from another revision opens its original stored code and clearly identifies that revision; it is never moved to the same line number in the latest snapshot. M3 remains the owner of any explicit re-anchoring behavior.

### 4. Keep state alive when annotation DOM is recycled

- Move the minimal affected composer/reply/action state to review-lifetime ownership outside virtualized annotation mounts. Keep the existing discussion controllers/`useSubmit` semantics; do not build a generic draft cache or retry-key registry.
- Stable metadata IDs identify that state, but a React key alone does **not** preserve it after virtualized unmount. Scrolling away, collapsing a file, changing navigator tabs or temporarily hiding a thread must not discard text or replace an unchanged retry key.
- Preserve the frozen new-discussion anchor once typing begins. A second gutter action cannot silently move or erase a typed draft; retain the original context and offer an explicit return/discard path. Keep the current single-active-new-discussion behavior rather than inventing a persistent multi-draft outbox.
- Preserve **independent reply and status mutation identities** and their combined busy/error behavior. Lost reply response → resolve/reopen → scroll away/back → unchanged retry must still persist one reply.
- Opening a new composer focuses it with `preventScroll`; use the viewer's navigation API only if a small reveal is necessary. Do not focus every time an existing annotation remounts.

### 5. Replace the permanent right column

```text
Review toolbar: review / snapshot / capture / Review decision / settings
Left navigation: semantic navigation + Files | Comments
Main area: CodeView header → diffs with inline threads → CodeView footer
```

- The left Comments index lists real discussion targets, revision/range, status and a message excerpt. Selecting a code thread reveals its file/range and inline thread; selecting a non-code thread opens its existing topic, evidence, finding, diagram element or review context in the main area.
- Preserve review-wide/selected-topic scope and access to resolved and historical discussions. Do not strand non-code anchors or attach them to an arbitrary first code file. Non-code composers/threads live alongside their main-area context, not in a restored right sidebar.
- Keep Topics/Findings and Text/Diagram/Evidence access; simplify the source-review composition without erasing semantic review or independently reconstructing its rules.
- **Review decision** opens an accessible modal dialog reusing `Decisions`, validation and history. Closing/reopening retains a dirty draft and its exact revision; posting still uses the existing human-only endpoint. Return focus to the trigger on close.
- Move `Sessions` under **Settings → Integrations** as a secondary dialog/section. Preserve explicit association/removal and unavailable-backend reporting, with no runtime launch/stop or agent-conversation claim. Reviews never wait for this query; poll only while the integration surface is open.

### 6. Explicitly enable Highlights

- Select `preferredHighlighter: 'highlights'` consistently before the first code highlighter initializes. Use `canUseWASM()` for the documented WebAssembly/SIMD capability check; unsupported environments use `shiki-js` without breaking review.
- Only one backend can be active in a JavaScript realm. An already initialized Shiki instance ignores later preferences: configure all code renderers consistently and verify `getHighlighterType()` on a fresh startup. A dependency in the lockfile is not proof that Highlights runs.
- Keep the existing pinned integration first; Diffs already brings Highlights. Do not add a direct dependency unless using a direct Highlights API, and do not write a custom HTML/token renderer.
- Start without a worker pool: this matches the current app and Pierre's recommendation for Highlights. Add workers/batching only for a measured remaining problem, not by copying DiffsHub's stress-test architecture.
- Use supported bundled palettes and normal filename/language resolution. Unsupported languages retain readable plain text through the library; do not download grammars or register custom TextMate themes as if they were Highlights palettes. The packaged app must load its bundled highlighting assets without a CDN.

## Delivery sequence

1. Reproduce the current scroll jump with a realistic long-file fixture; record the integration-level failure. Confirm the pinned API seams and actual highlighter selection.
2. Extract the code surface and introduce controlled CodeView ownership with stable IDs/versions, source scope, built-in headers and header/footer states. Retain explicit unavailable-file rendering.
3. Separate existing discussion state/actions from placement; wire gutter creation, inline annotations, durable threads and exact-range navigation. Prove recycling/retry behavior before removing the old placement.
4. Move comment navigation left, decisions to the trigger dialog, and sessions to integrations. Remove the right-column layout and obsolete detached code-comment action once their replacements work.
5. Enable Highlights consistently, align source/annotation styling with the reference, and validate the full packaged/native path.

These are reviewable implementation steps, not parallel alternative UIs to retain permanently. No server schema/API change is currently required. Recheck that assumption against the deployed M3 contract if M3 lands first.

## Verification plan

Reuse the actual Go server, disposable two-repository fixtures and existing Playwright/native smoke setup. Do not alter the user's worktrees or live Rex sessions.

- **Scroll/placement regression:** long file, line/range well below the top, hover/keyboard `+`, single/multiline BASE and HEAD, split/unified, selection → composer → typing → post → reply/resolve → SSE. Assert the anchored code stays in view and composer/thread geometry is below the correct side/range; no file-top reset. Also exercise dynamic header/footer height changes and an empty CodeView.
- **Navigation/context:** Files ↔ Comments, same paths in different repos, multiple threads on one location, collapsed/context-expanded lines, old-revision threads while browsing a new revision, non-code anchors, binary/omitted files and both file/directory replacement directions. Left navigation and inline context must agree on canonical IDs.
- **Lifetime/failure:** scroll an active draft/reply outside the virtualized window and back; collapse/reopen and switch tabs. Retain text, anchor and retry identity. Extend the real committed-but-lost-response test with recycling and an intervening resolve/reopen; require one immutable reply. Retain stale If-Match, disconnection/reconnect, safe Markdown and frozen-draft tests.
- **Decisions/integrations:** opening/closing the decision dialog, dirty draft after capture, correct verdict/revision/history and focus return. Settings-only session access, independent unavailable Rex state and no polling after close.
- **Highlights:** cold-start assertion of the actual backend, TS/Python/Go and an unsupported language, realistic large-file rendering, capability-check fallback and packaged MyGo/WebKit asset loading. Verify the native Mac path as well as Linux WebKit; call out any Mac-only validation still unavailable.
- Update existing tests for the new placements without deleting behavior assertions. Run client formatting/lint/unit checks, TypeScript/production build, all browser scenarios and native smoke; check generated types only if the API changes. Run affected Go checks/builds if shell/packaging changes become necessary.
- Inspect source-mode screenshots against the supplied DiffsHub page at comparable viewport sizes. Test without a right sidebar and with a visible inline thread; importing Pierre libraries alone is not visual/interaction acceptance.

The original planning commit performed documentation/reference inspection only. Implementation evidence and validation are recorded below.

## Draft choices recorded before implementation

These proposals were recorded in planning commit `6561918`. The implementation decisions below supersede their open choices.

1. **Source-mode composition:** a continuous CodeView and Files/Comments switch, while retaining semantic navigation/representations. This is the closest fit to the supplied reference; would ask whether the existing two-representation composition should remain exposed in source mode or only semantic modes.
2. **Decision/session surfaces:** native modal dialog for decisions and Settings → Integrations for sessions. This satisfies on-demand decisions and secondary runtime access without adding a UI framework; would ask whether a decision popover is preferred over a modal. Dirty drafts must survive either choice.
3. **Historical navigation:** show the thread's original code revision and make that snapshot context explicit when navigating from the Comments index, rather than projecting it onto the latest file. Would ask whether selecting a historical thread should also change the global revision picker or just the code scope; never retarget a dirty decision draft.
4. **Visual defaults:** propose split diffs with a unified toggle and a bundled Pierre light palette initially, matching the supplied source-review reference. Would ask about preferred defaults; no full theme-management subsystem or new persistent preference is justified merely to match the example.
5. **Controller ownership:** preserve state above recycled annotation views, with stable draft/thread IDs and existing independent mutations. The precise factoring must stay within the discussion domain; the concrete requirement is draft/idempotency survival, not a generic state framework.
6. **CodeView/highlighter integration:** controlled items, stable presentation versions and callbacks, supported header/footer/gutter/annotation APIs, Highlights with the documented capability fallback, no new worker pool by default. These follow the current state owner and pinned Pierre APIs, not speculative scale requirements.
7. **Milestone naming/order:** M3b is recorded exactly as requested and kept separate from M2b #5. No new M3 prerequisite or server contract change is inferred from the name; confirm scheduling when approving implementation.

## Implementation record

- Replaced the single-file `MultiFileDiff` integration with controlled `CodeReview.tsx`/Pierre CodeView. Source mode is a continuous, virtualized list; semantic views still use their existing representation/evidence path. Native headers/statistics, measured header/footer slots and split/unified rendering remain Pierre-owned.
- Source item identity is revision + repository + path. Selection updates do not issue navigation requests. Explicit navigation waits for the source collection/file reads to settle, expands a collapsed target, then uses CodeView's item/range API once. Typing, SSE and presentation updates do not replay that request.
- Code composers and saved threads use side-specific annotations beneath their final anchored line. One annotation groups all discussion/composer IDs at the same side/end line; each retains its own complete range and controller. Request ranges are projected once into the five input fields.
- Discussion controllers stay mounted for the review and portal their views into Pierre annotation slots or non-code context. Recycling/collapse removes only view hosts, not reply text or the existing independent `useSubmit` hooks. Hidden drafts participate in review-change/before-unload warnings. Nothing beyond preferences is persisted locally.
- Files/Comments navigation and settings remain visible outside the sidebar's scrolling content. Files and Comments retain their models/filter state across tab switches. Non-code comment navigation opens its record context and focuses its thread.
- Removed the permanent right column. Decisions use a native modal with focus return and retained dirty state. Sessions mount only under Settings → Integrations; their existing polling ends when closed.
- Highlights is verified through the actual shared highlighter type on cold startup. Added only `wasm-unsafe-eval` to the CSP, not JavaScript `unsafe-eval`. No new dependency, theme registry, worker pool, API, migration or server logic.

### Defect evidence and limits

The pre-change long-file test kept a selected line visible in Chrome: selection alone did **not** reproduce the reported jump. Its inline-composer assertion failed because the old composer was outside the file surface. The replacement is validated through real pointer-range selection, composing/typing/posting, SSE, navigation and reload; no claim is made that the original macOS-specific symptom was independently reproduced.

Implementation checks also exposed and corrected two integration failures: navigation consumed before earlier source items finished loading, and repeated optimistic query selection producing new parsed-metadata objects during cached-revision switches. Navigation now waits for a settled list, while presentation reconciliation uses immutable snapshot identity rather than parser-object identity.

### Implementation decision ledger — least confident first

1. **Historical comments change code scope, not the global picker.** Their original revision is explicit in the code context and comment label. A dirty decision is never retargeted. If asking were free, would ask whether the global picker should follow historical code navigation instead.
2. **Source is one code canvas; semantic modes retain two views.** The requested DiffsHub-like source workflow takes the main area without discarding topic/diagram/evidence functionality or changing saved semantic preferences. Would ask whether a source-specific secondary representation should remain exposed.
3. **Native decision modal; settings opens integrations.** Reused existing decision/session behavior instead of adding a UI framework. Untouched COMMENT forms follow the selected revision; edited verdict/text/references freeze the draft revision. Closing does not clear it. Would ask only about modal versus popover presentation.
4. **Split/light/readable defaults, no new preference fields.** Use bundled `pierre-light`, 13px code and a local unified toggle. No custom Highlights theme, theme management, code editor or batch pending-review workflow.
5. **Accessible gutter slot plus a range form.** The pinned library forbids combining `onGutterUtilityClick` with `renderGutterUtility`. Use its custom slot for a labelled button and its selection/hover getters, plus a labelled numeric BASE/HEAD range form for keyboard access; do not recreate hit-testing or drag selection.
6. **Review-lifetime controllers with view-only portal hosts.** This preserves existing mutation hooks, rather than creating a generic draft/retry registry. Multiple code views can share a controller. Drafts survive view recycling, not a completed app reload; leaving/reloading warns rather than writing sensitive drafts to storage.
7. **Group annotation presentation by side/end line.** Pierre names slots by location. Grouping IDs into one annotation avoids duplicate location slots while keeping each discussion's anchor, messages, status and retry state independent.
8. **Treat stored source identity as immutable.** Query observers can reselect cached content into a fresh parser object. That must not cause a render loop or remount. The server's immutable snapshot tuple is the content identity; annotation/collapse changes alone bump presentation versions.
9. **Keep explicit source fallback and conversations accessible on read failure.** If exhaustive listing fails, an already-grounded selection can still fetch its exact stored file, with a partial-list notice. If that file fetch fails, its messages/drafts appear under an unavailable-source heading and move inline after recovery without losing state.
10. **Use React render callbacks for changing context, stable options for code mechanics.** A permanently stable header callback would leave CodeView's memoized React slot stale. Updating the React header/footer content does not replace files or add another scroll/measurement system.
11. **Enable WASM narrowly and verify the real engine.** CSP was the observed reason the first browser run selected `shiki-js`. The supported-runtime path now selects Highlights; a capability-disabled test verifies the real JS fallback. Existing JavaScript-evaluation restrictions remain intact.
12. **M3b is a client milestone on this request.** M2b remains pending and required before M3; revision-aware comparison/re-anchoring remains M3 work. No compatibility layer or server contract change was added.

### Validation

- Client formatting, lint, two existing model tests, TypeScript and production build passed.
- Eighteen real-server browser scenarios passed: the eight existing scenarios retained their behavior assertions, plus ten code-review cases. Coverage includes pointer multiline selection, BASE/HEAD anchoring, gutter creation, code-context geometry, SSE, same-location threads, old revisions, actual TS/Python/Go highlighting, unsupported-language/JS fallback, virtualized/collapsed drafts, a committed-but-lost reply followed by resolve/recycling/retry, cancelled navigation with a hidden draft, read-failure recovery, non-code navigation, empty CodeView and stopped session polling.
- Native Linux MyGo/WebKit smoke passed with bundled assets, Go-backed REST/SSE, actual Highlights, Pierre Trees navigation, a durable inline code-range comment and the decision modal.
- Frozen install/peer check and generated-type freshness passed without dependency/API changes.
- Client Go formatting/vet/race tests and Linux/darwin-arm64 builds passed. MyGo Linux packaging and unsigned darwin-arm64 app packaging passed with the pinned SDK.
- Compared `/tmp/prunelle-m3b-implemented.png` and the native capture against the supplied DiffsHub reference. The main code area uses the same native diff/header/selection primitives, with Prunelle attribution and durable thread controls rather than demo personas.

### Remaining verification / known warnings

- Native macOS launch, signing/notarization and a live tailnet streaming session remain unverified here. The unsigned Mac bundle is cross-built, not a native-Mac test result.
- Chrome development runs emit bounded `ResizeObserver loop completed with undelivered notifications` messages during some rapid annotation/revision transitions. Functional/geometry checks pass; no global error suppression or custom observer/scroll loop was added. This warning is not represented as a resolved library defect.
- The existing large-bundle warning remains. Maximum-capacity performance and a separate browser 5xx/409 unknown-outcome presentation scenario were not benchmarked/added; real lost-response idempotency and disconnection/reconnect paths were exercised.

## Scoped ship simplification

Architecture review retained CodeView's required item versions, the keyboard range control and review-lifetime discussion controllers: removing them would change observable behavior or discard mutation/draft state during recycling. No product behavior or public API was removed, and tests were left for the Test phase to curate.

### Simplification decisions — least confident first

1. Use one stable `FileContent` parser selector rather than JSON-round-tripping file identities to memoize per-file selectors. The existing required response fields are authoritative; `Service.GetFile` fills revision/repository/path directly from the request. Query keys, null-side/binary/omitted handling, snapshot IDs and async annotation guards stay unchanged.
2. Reuse the controlled selected range for explicit navigation, and pass the keyboard form its already-chosen file/content instead of letting it reconstruct selection from entire collections. Preserve the existing selected-file/first-file fallback and validation.
3. Keep one immutable item-ID comparison rather than also checking the identical parser cache key; retain annotation/collapse version reconciliation. Simplify redundant header callback union types without changing supported item kinds.
4. Alias the existing composer action directly and remove only the unused single-file header CSS. No new dependencies, stores, retry registries or compatibility paths.

Focused rerun passed: client formatting/lint/typecheck, two model tests, all 18 existing real-server browser scenarios, production build and native Linux MyGo/WebKit smoke. Existing ResizeObserver and bundle warnings remain visible. No tests were added/removed/restructured, and no commit was made; ship owns the remaining workflow. Native macOS/live-tailnet checks remain unverified as above.

## Behavioral verification rationale

The browser scenarios exercise real disposable Git snapshots and the public review API; network interception loses a real committed response or makes an actual read unavailable. Native smoke additionally exercises bundled MyGo/WebKit and an external reply received over Go-backed SSE without a local action/refetch. No production behavior, API, dependency or test configuration changed.

One scenario adds the missing boundary between an unposted inline draft and a newly captured snapshot: returning to the draft must restore its original source without moving the global picker, and posting must retain the original revision/range/text. Six existing scenarios were strengthened: inline placement/context, virtualized drafts, hidden retry state, binary source identity, integrations lifecycle, and native transport/anchors. No whole tests were removed or consolidated.

### Counterfactuals

Each row names wrong observable behavior that must fail, and a behavior-preserving change the test should survive.

| Scenario                                           | Plausible regression detected                                                                                                                                                                       | Refactor it should survive                                                                                                                                                                |
| -------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Long-file selection/composer/post/SSE              | Pointer range or typing jumps to the file top; composer is detached/far below its final line; posted/live conversations lose context.                                                               | Different annotation/state ownership, form wrappers or small layout/measurement changes: assertions use viewport, rendered-line-relative geometry and persisted ranges, not exact pixels. |
| BASE multiline/unified/gutter                      | BASE text is anchored as HEAD, multiline endpoints are truncated, or gutter comments use a stale line/side.                                                                                         | Parser/query refactoring, different token spans or layout internals behind Pierre's supported split/unified and gutter interactions.                                                      |
| Virtualized draft across files/tabs/collapse       | Navigating to a distant file or collapsing the original destroys/reanchors the unposted draft.                                                                                                      | Keeping offscreen DOM mounted, replacing portal storage or changing virtualization: only visibility, recovered text and the durable anchor matter.                                        |
| Inline draft after capture                         | Capture drops the draft or silently posts it against the newer snapshot; Return to draft changes the global revision picker.                                                                        | Different cache/state owners and historical source-loading mechanics while preserving original-snapshot navigation and posting.                                                           |
| Inline lost-reply/resolve/recycle/cancelled switch | A recycled reply gets a new retry key and duplicates a committed message; hidden drafts no longer block leaving.                                                                                    | Different controller/portal lifetimes or retry-state storage with the same public warnings, request identity and single durable message.                                                  |
| Same-location conversations                        | One conversation overwrites another at the same side/end line or the navigator opens the wrong thread.                                                                                              | Different annotation grouping/host allocation while both independent conversations remain visible and navigable.                                                                          |
| Highlights TypeScript/Python/Go                    | An advertised Highlights backend renders all source as plain text, or a supported language never tokenizes.                                                                                         | Palette/color changes and token-span segmentation; no exact theme color, token count or private highlighter instance is pinned.                                                           |
| No WASM/unknown language                           | Unsupported WASM crashes instead of using JS; unknown source disappears or executes markup.                                                                                                         | Capability-check/fallback implementation and language resolution changes preserving readable inert source and the reported actual engine.                                                 |
| Cold source read/recovery                          | Source failure hides its conversations or recovery discards an in-progress reply.                                                                                                                   | Query-library, loading markup or annotation-host refactoring preserving the unavailable context and recovered draft.                                                                      |
| Non-code navigation/scope                          | Clicking a topic comment opens the wrong context, fails keyboard focus, or resets scope on Files/Comments switches.                                                                                 | DOM wrappers, CSS classes and navigator-state ownership changes preserving accessible headings, focus and selection.                                                                      |
| Empty source/integrations lifecycle                | Empty reviews lose context/footer, allow a decision without a snapshot, or sessions poll while closed/do not poll while open.                                                                       | Header/footer composition and polling scheduler changes; virtual time checks observable requests rather than a particular 5-second delay.                                                 |
| Topics/grounding/perspectives                      | Grounding opens the wrong captured line, unassigned repositories disappear, or local presentation preferences do not restore.                                                                       | Cache/query normalization and extracting semantic/source presentation components without changing canonical records or accessible navigation.                                             |
| Repository/revision source identity                | Same paths collide across repositories, file/directory replacements fail, failed refresh clears selection/cache, or working-copy edits leak into a snapshot; binary files offer fake text comments. | Different source-header/cache/tree composition while preserving exact repository/path/revision content, selection, keyboard access and honest binary notices.                             |
| Code discussion across semantic views              | A range loses topic membership, replies/resolution vanish after changing views, or reload loses the durable conversation.                                                                           | Different view/state ownership and query invalidation while the same public discussion remains available across contexts.                                                                 |
| Semantic lost-reply/resolve                        | A topic-hosted failed reply is lost/duplicated by resolving, or its unchanged retry identity is replaced.                                                                                           | Moving/splitting discussion controllers or mutation state while retaining the semantic discussion surface and one persisted reply.                                                        |
| Evidence/historical ranges                         | Response-only anchor fields are reposted and rejected, or reopened historical comments silently target current source.                                                                              | Different anchor projection helpers/parser objects/cache representation while preserving the complete server-owned immutable anchor.                                                      |
| Decisions/history/working tree                     | Request changes submits without actionable references; closing the modal loses/retargets a dirty decision; old source/history changes with the worktree.                                            | Extracting modal/form components or state/cache ownership while retaining revision-specific payloads, focus restoration and immutable source.                                             |
| Lost discussion/restart/reconnect                  | Retrying a committed creation duplicates it, cached conversations disappear offline, or missed/later events never arrive after a real restart.                                                      | Transport/SSE parser/invalidation refactoring with unchanged explicit retry identity, durable state and live public updates.                                                              |
| Topic drafts/Markdown/stale resolution             | Topic navigation retargets a draft, Markdown executes HTML, or a 412 discards the reply instead of refreshing before explicit retry.                                                                | Different Markdown, draft or query implementation preserving safe output, original anchors and stale-state recovery.                                                                      |
| Bundled native smoke                               | Browser-only success hides broken desktop assets/Go transport, wrong stored HEAD range or stalled native SSE after its initial connection.                                                          | Bundle chunking, component extraction and transport internals preserving WebKit source/Highlights/tree interactions, API anchors, external live reply and dialog behavior.                |

### Test-design decisions — least confident first

1. Stable code context is bounded by one rendered line of movement while typing, not an incidental three pixels. The empty composer must begin within two rendered lines beneath its range; viewport/range assertions still reject the original detached/top-jump failures.
2. Retain semantic and inline lost-reply cases: the former exercises the non-code discussion host, the latter adds CodeView recycling and hidden-draft navigation protection. Likewise cached list-refresh failure and cold file-read failure protect distinct available-data states, not duplicate error rendering.
3. Retain Pierre custom-element/data selectors only where needed to identify exact repository/path or code-side geometry inside its shadow DOM. Use accessible controls elsewhere. Avoid assertions about internal item versions, parser identities, controller instances, portal counts or DOM eviction.
4. Use virtual time to observe open polling and its cessation; do not sleep for the current interval. The native external reply uses the real local server, not a fabricated stream. Remove the unasserted absolute-path browser screenshot side effect; standard failure traces/screenshots and native capture remain available.
5. No production fix was needed: the new original-snapshot draft and native SSE assertions pass the existing implementation. Binary notices are checked against the API's `BINARY` omission reason, not the client's fallback copy. No release prerequisites were assumed verified: native Mac launch/signing, live tailnet streaming, maximum-capacity performance and separate browser 5xx/409 unknown-outcome presentation remain gaps.

### Verification results

- Frozen install/peer checks and generated API freshness passed unchanged.
- Formatting/lint, both model tests and typechecked production build passed. The native script uses `globalThis.fetch` to honor the existing ESLint environment; no lint/config exception was added.
- All 19 real-server browser scenarios passed, including the new capture/draft boundary and the corrected binary assertion. A prior invocation was terminated with exit 137 before completion; a completed intermediate run exposed the incorrect fallback-copy expectation, corrected to the canonical API reason before the final full pass.
- Native Linux MyGo/WebKit smoke passed, including exact stored HEAD/revision/repository/range and an externally posted reply delivered over Go-backed SSE.
- Client Go formatting/vet/race tests and darwin-arm64 build passed. Existing ResizeObserver and bundle-size warnings remain visible; native Mac/live-tailnet checks remain unverified.

## Approved navigation corrections

R1 and R2 are confined to the existing source-navigation and in-memory draft flows. The user explicitly approved adding `client/src/SourceTree.tsx` to the original file scope. No server/API, dependency, persistence or retry behavior changed.

### Decisions — least confident first

1. **Use the pinned tree's host activation surface, not a new selection/scroll model.** Keep selection changes and programmatic selection synchronization intact. An unmodified activation of an already-selected file explicitly calls the existing navigation action. Native row buttons emit clicks for pointer/Enter/Space; search Enter consumes the key instead, so its capture handler uses the public focused-file model. Read the row's canonical path through the composed event and validate it with the public model; do not mutate the shadow DOM, navigate on focus/arrow movement, alter modifier/folder handling or track scrolling. Reactivation uses the existing file-navigation contract (file start, not a retained line selection).
2. **Retain the diagram owner's authoritative topic ID as draft-only context.** The diagram producer already receives `Representation.topic_id`; carry it alongside the target and freeze it through the existing first-edit draft mechanism. Return passes that topic through the same diagram navigation path used by saved discussions. Other anchor kinds need no topic metadata. The server still determines persisted membership, and submissions still contain only revision, anchor and body; no cache scan, new endpoint or persistent field is needed.

### Regression evidence

- R1: select `long.ts`, scroll to a distant file without changing tree selection, then activate the still-selected entry by click, keyboard or search Enter. Each must return the original file to view. The click failed before the fix; search Enter also failed when only native-click activation was handled. The test tolerates different virtualization/cache/controller implementations and does not assert item versions or scroll counters.
- R2: start a real fixture diagram-element draft, navigate to another topic and Files, then return through Comments. Require the original topic/diagram/selected node and text, then verify the persisted original revision/representation/element and server-derived topic. The original implementation failed to restore the topic. The test tolerates different draft/portal/query ownership without weakening the immutable-anchor contract.
- Validation passed: both focused regressions, all 21 real-server browser scenarios, native Linux MyGo/WebKit smoke (including real external SSE), formatting/lint, both model tests, TypeScript/production build, frozen install/peer checks and unchanged generated API types. Client Go formatting/vet/race and darwin-arm64 cross-build also passed. No commit was made; native Mac/live-tailnet and the previously documented warnings remain separate verification gaps.
