# UI review — 24–25 October 2026

Reviewed Connections, Requests, Commands, Settings, pairing, command editing,
command permissions, invitation completion, and the locked panel. Desktop and
390 px phone inspection were followed by 320 px and keyboard checks.

The review keeps the dark palette and quiet layout, and addresses the details
that make the interface feel unfinished or generic.

| Area | Issue | Correction |
| --- | --- | --- |
| Icons | Text arrows, gear, plus, close, check, and external-link symbols depend on the system font. | One local SVG sprite, consistent sizes and strokes; official provider assets retained. |
| Disclosures | Tiny glyph arrows do not rotate when opened; spacing varies. | Shared SVG chevron with visible open state and larger hit area. |
| Type | Important labels and actions are 10–11 px. | Clearer 12–14 px hierarchy; larger form controls and secondary text. |
| Buttons | Close/settings controls are small; disabled controls imply loading even when unavailable. | Consistent targets and distinct disabled/loading states. |
| Dialogs | Long forms hide both close and submit controls during scrolling. | Fixed heading/footer with a scrolling body, bounded to the viewport. |
| Mobile commands | Actions wrap into an awkward vertical stack beside a narrow command path. | Full-width command information and one action row below it. |
| Long values | URLs, paths, account names and command definitions can crowd adjacent controls. | Flexible text columns and wrapping code previews without page overflow. |
| Inputs | An existing input's full editor makes the command dialog excessively tall. | Compact expandable input rows; open new inputs for editing. |
| Input choices | Choices textarea lacks an accessible label; input names have no client validation. | Explicit labels and constraints; focus new inputs. |
| Clipboard | A failed clipboard write leaves only a truncated invitation available. | Reveal a selectable invitation only when copying fails. |
| Form defaults | Pairing reuses prior account/automatic choices and open disclosures. | Fresh safe defaults for each new invitation. |
| Permission state | Invitation access badge can say Restricted after selecting commands. | Badge follows the actual selected scope. |
| Permission choices | Bare checkboxes, long paths, and consent blend into one text block. | Distinct selectable rows and a separate consent area. |
| Permission context | Command access dialog does not identify the connection being edited. | Show app name and exact website origin. |
| Settings | Commands permission mode is absent despite being implemented. | Describe its OS-account access alongside other modes. |
| Errors | Toasts outside a modal can be hidden by the browser's top layer. | Inline, announced errors inside the active dialog. |
| Feedback | Success and failure share warning styling; copy feedback is inconsistent. | Neutral success feedback, explicit error treatment and busy state. |
| Repeated submits | Submit forms do not disable their submit button while saving. | Disable the actual submitter for the duration of its request. |
| Polling | Replacing lists can drop keyboard focus and reset output scroll position. | Restore keyed focus and output scroll after updates. |
| Navigation | Current section is only communicated visually. | Set aria-current and provide a keyboard skip link. |
| Requests | New history can obscure older approval requests; cancellation reasons are inconsistent. | Show approvals first and retain meaningful terminal explanations. |
| Destructive actions | Removing a command/disconnecting/clearing history happens immediately. | Short contextual confirmation with an explicit action label. |
| Offline state | Header can continue to say Ready when the runner cannot be reached. | Persistent offline status that clears on reconnection. |
| Empty states | Dashed generic boxes, punctuation and actions vary between screens. | Quiet, consistent empty states with a useful next action. |
| Motion/focus | Textareas lack the shared focus treatment; motion preferences are ignored. | Consistent focus rings and reduced-motion support. |

All corrections above are implemented. Verification covered:

- Desktop at 1185 × 1223 and 1280 × 720; phone layouts at 390 × 844 and 320 × 640, without horizontal page overflow.
- Actual SVG chevrons, including the 180-degree open state; no decorative Unicode arrow/gear/close/check glyphs remain in the control UI.
- Expanded input editors, fixed dialog headings/actions, visible server-validation errors, permission selection and required consent.
- First-run empty/file-locked states, locked panel, offline runner, Escape/cancel, focus return, current-page/skip-link behavior, and focus retained across a real polled state change.
- Existing browser pairing and command approval/output flow; no provider inference calls. No console errors/warnings in the final review session.
- Go formatting/vet/race tests, 34 SDK tests, both HTTP end-to-end suites, 35 palette contrast checks, and six platform/architecture package builds.

The visual browser review ran on macOS. Native Windows/Linux execution, other browser engines, OS-specific clipboard denial behavior and an independent accessibility/security review remain release validation. Clipboard denial has a manual-copy fallback. Native provider eligibility/isolation/image blockers remain described in provider-review.md and commands.md; the UI cleanup does not enable those paths.


## 25 October: groups, sessions and connection requests

- Connection requests appear above the other Connections content in every authenticated panel, with browser-observed origin/account, direct Connect/Reject, a navigation count and tab-title count. Tested rejection and approval from a second open panel in Chrome, including subsequent signed website requests. The control window never grants command or automatic access as part of this flow.
- Group preferences support standalone groups and optional nested subgroups, AI badges, command-only selectors, nearest-parent inheritance and local model/reasoning preferences. Tested live child-label updates after a parent selection; native unavailable states remain explicit.
- Phone preference summaries stack labels/selections without stranded badges. Reviewed 390×844 and 320×640 with no horizontal page/dialog overflow; modal heading/actions stay reachable while the body scrolls. All chevrons remain SVG.
- Session controls, immutable message numbers, actual execution details, configured-version provenance, bounded diagnostic disclosure and four sharing switches use the existing concise UI. Request details preserve their disclosure state across refreshed snapshots. Closing during SDK connection/session opening cannot submit late work.
- Validation: Go formatting/vet/race checks, 39 SDK tests, real HTTP suites covering continuing process/context, inherited routes, live metadata omission and normalized/redacted errors; 35 palette contrast checks; six package targets. Logo work remains with the project owner.

Interactive pairing needs the unlocked local panel in the same browser. Embedded webview popup support, site opener isolation and browser local-network rules can require the existing manual invitation fallback. Native execution/continuation, provider eligibility, image automation, OS signing and native Linux/Windows runtime verification remain release blockers rather than hidden fallbacks.

## 25 October: visual hierarchy and everyday use

- Connections puts incoming requests and paired applications first. App rows show the saved default or group choices, granted scope, approval policy and meaningful Paired/Paused/pending/working states. Installed clients occupy a compact strip below the apps; their links open the relevant Settings explanation.
- Replaced repeated boxed cards and initial-letter avatars with separated rows, local browser icons and an app-to-destination layout. Kept the generated dark palette, supplied WDC logo and official provider assets; provider marks now share consistent frames and optical sizes.
- One Configure action per app; pause, automation, command permissions and disconnect live in a keyboard-accessible native disclosure menu. Existing consent and destructive confirmations are retained.
- Preferences uses a wider dialog with visible group selectors, parent indentation and explicit inheritance/custom-choice labels. Optional model/reasoning controls remain collapsed. Default option labels describe the inherited fallback even when the group has its own choice.
- A command-access shortcut returns to the unsaved preference draft; changed revisions still require review. Unavailable native routes and stale command grants remain visible and fail closed.
- App-initiated pairing is the first route presented. Manual invitations remain available with safe fresh defaults, clear account-ID guidance and focused copy feedback.
- Requests separates approvals, active work/sessions and compact expandable history. History includes dates for older requests. Program details are shown only for matching pinned definitions; command approvals retain the OS-account permission notice.
- Settings leads with working sharing/device controls. Provider diagnostics and permission/privacy explanations follow. Removed the permanent advisory banner and redundant Settings links.
- Preserves disclosure state, output scroll and keyboard focus during updates. Menus dismiss outside/on Escape, errors remain inside dialogs, and controls stay reachable in narrow scrolling forms. All select arrows use the bundled SVG chevron.
- Browser checks exposed a cross-port cookie collision between local installations. Owner sessions now use origin-scoped storage and an explicit header, with independent CSRF protection; no ambient cookies are issued or accepted. Image downloads use the same authenticated fetch before saving a blob. The signed/encrypted SDK transport is unchanged.

Verification used isolated fixture state inside this repository, including examples named Rorido/Kings; no changes were made to either application. Browser checks covered desktop, 390 px and 320 px layouts, first-run/file-locked states, pairing, command validation/focus, group inheritance, the permission-editor round trip, history disclosure through state updates and two independently authenticated local panels surviving refresh. The Go/race, 48 SDK tests, both HTTP suites, CLI lifecycle, 35 palette checks and six platform package builds passed. Go regressions cover explicit session credentials, instance isolation, logout, and byte-exact authenticated image downloads. No inference requests were made for this UI work.

## 25 October: Claude availability

Removed the blanket Claude eligibility block and stale disabled explanation. The Settings and Connections status now comes from the installed client's native login, version and isolation compatibility checks. Verified both clients show Ready, Claude can be saved as an app default, inherited group labels update, and Sonnet/Opus/Haiku choices appear in optional model preferences. No API credentials or new sign-in UI were added. The separate Claude native integration test used real subscription inference; its results are recorded in validation.md.

## 25 October: card layout and simpler connection controls

A second design agent reviewed the whole interface and implemented the shared card styling. The changes stay within the bundled HTML, CSS and JavaScript, using the existing dark palette, logo and official provider icons.

- Connections, incoming connection requests, running requests, history, sessions, commands and providers use bounded cards with consistent spacing. Settings group related controls in cards without a divider after every switch.
- Each connection leads with its app name and exact website origin. **Default program** opens **Program settings**; **Permissions** is visible beside it, with a summary of approval policy and file access. **Connection details** in the menu retains technical identity information; folder and command access remain available there.
- **Permissions** has one draft and one **Save changes** for automatic approvals, file transfer and folder grants. Folder access and the picker are views of the same modal; Back and Escape preserve edits while navigating. Dirty dismissal offers **Keep editing** or **Discard changes**; clean dismissal closes immediately. Saving blocks editing/dismissal; validation and persistence rejections apply none of the settings. Lost acknowledgements use receipt recovery as described below. The server validates and persists changes together. Content uses the standard padded, scrollable dialog body. Routing labels describe task-specific choices and their effective program instead of override/inheritance terminology.
- Dialogs dismiss on a backdrop click; Permissions asks before discarding a dirty draft. Clicks inside blank modal padding and drags starting inside the modal do not dismiss it. Escape, close buttons, cancelled confirmations and keyboard focus restoration remain supported.
- Empty states use short, direct copy. Connection requests retain their exact origin, optional account details and explicit Connect/Decline controls. History remains expandable, with approval requests presented first.

Browser verification uses disposable state in `.local/card-ui-qa`, not a real app connection. It covers all four screens at 320px, the Connections layout at 390px and desktop, static/dynamic modal dismissal, cancelled automatic consent, focus return, and a completed local connection test. No AI inference requests were made for this redesign. Chrome on macOS was exercised; other browser engines and native Windows/Linux UI remain external runtime checks.

## 26 October: folder access and selection

- Folder selection is a view inside the existing access dialog, without a second backdrop or stacked modal. Back preserves the draft; closing or cancelling does not change grants.
- Home/Up controls, clickable breadcrumbs, folder icons and search distinguish navigating from selecting. The action names the folder being added. Its location trail appears before selection, and its full canonical path is visible in the access card before saving. Additional folders start near the last selection.
- Entering a path is optional. The entered location must open successfully before it can be added; loading, failed and edited paths cannot select a stale folder. Empty folders remain selectable, duplicates are explained, and the bounded listing reports truncation.
- Protected locations are visibly unavailable for sharing, using the same server-side boundary as saved grants. Owner-only folder listings still require the authenticated control session; browsing never grants access.
- Folder cards show read/edit access and optional edit automation. New folders remain read-only. Unsaved changes are explicit, and Save stays disabled when settings match their original values.

Verification uses a local fixture app and folders under `.local/folder-ui-qa`. It covers navigation, search, invalid paths, empty folders, duplicate selection, staged grants, edit defaults and explicit saving. No real application folder grants or AI requests are needed for these checks.

## 6 November: stale drafts and uncertain permission saves

- Permission saves include the draft's permission revision. A change in another window or a separate permission review rejects the entire stale save. **Saved settings changed** freezes edits and offers **Reload saved permissions**, with confirmation before replacing the draft. A stale folder list cannot restore revoked access.
- Each save has a durable, payload-bound receipt. A lost response triggers a read to confirm the outcome, without repeating the write. If confirmation is unavailable, **Save not confirmed** offers **Check saved permissions** or an explicit **Retry save** with the original ID and payload. Frozen drafts use **Keep draft** in confirmations. Closing explains that applied changes remain saved; it never claims an uncertain save can be discarded.
- Recovery messages and controls stay visible above the shared footer while content scrolls. Browser checks used disposable state at desktop, 390 px and 320 px, including lost responses, malformed acknowledgements, unavailable reads, a request lost before delivery, a later revocation, explicit reload and idempotent retry. Full Go race tests and all 91 SDK tests passed, alongside SDK type and generated-output checks. No provider inference was needed.


## 12 November 2026: connection setup, attention and access

- Approval cards and invitations offer a ready default program. It is saved through the existing routing API when the exact pairing appears; retries and reloads preserve the choice without overwriting newer routes. Unconfigured cards show **Setup needed**.
- First-run onboarding shows live programs and three connection steps. **Add connection** explains app, invitation and server routes. **Programs and models**, workflow defaults, command-only fields, sharing helpers and command setup use plain wording.
- Tab counts include connections, approvals and access requests. Optional desktop notifications handle browser permission, deduplication and navigation. **Always allow** confirms automatic approvals before saving and approving the current request.
- **Access** gathers automation, files, folders, commands and custom programs. Editors return to the hub, preserving consent and drafts. Returning to Programs and models preserves unsaved choices or reviews a newer revision. Cards include account IDs; the menu keeps four connection actions.
- Invitations created in a tab survive refresh, with copy actions and live countdowns. **API limitation:** no invitation listing/revocation endpoint exists. Cancel explains that removing this tab’s copy cannot invalidate a copied invitation. Custom programs require an explicit Access grant after pairing.
- Notices remain visible inside dialogs. Locking confirms and explains reopening; removal lives in Edit; narrow heading actions align below titles.

Verification used disposable `.local/ux-web` state on port 47431 and the sample app on 47432. Chrome/Chrome Beta covered desktop, 390 px and 320 px, real approval/manual pairing, saved defaults, invitation use, draft round trips, connection tests and locking, without horizontal overflow. Browser notification permission worked; OS banner delivery was not visually confirmed. The sample’s native-connect route briefly opened the installed panel; it was closed without approval. No AI inference was used. Preview servers were stopped.

Passed at that stage: 16 UI lifecycle tests (now relocated to `scripts/panel.test.mjs`), Go vet, bridge tests, all HTTP end-to-end suites and 35 palette checks. Go checks use the repository temporary directory to avoid macOS temporary-path alias failures.

## 12 November 2026: UX completion and isolation review

- Replaced the tab-only pending invitation list with authenticated server metadata. Reloads and other tabs see pending manual invitations; **Cancel** confirms and revokes the verifier before clearing the local copy. Claims, expiry and revocation disable an open copy dialog. Claimed connections are never silently revoked by cancelling an invitation.
- Added session-protected invitation list/revocation endpoints with CSRF checks; revocation requires exact Origin. Responses contain public pairing metadata and expiry, with no token or secret verifier. Server storage remains hash-only; **Copy again** stays available only in the creating tab's session storage. Existing restart invalidation is preserved.
- Moved UI lifecycle tests outside embedded assets, added an explicit runtime-asset embedding filter, and added regressions proving test files are absent from the embedded filesystem and return 404. `scripts/check.sh` runs the relocated tests and development isolation checks.
- Removed the sample's native-launch flow and required an explicit isolated endpoint for pairing and saved reconnection. HTTP/CLI suites reject the installed port, unsafe/nonempty data directories and stale runtime process IDs. Development instructions require an isolated fixture and never invoke an OS handler.
- Reviewed the complete UX/server diff. Automatic approvals now start off even when an app requests them; the owner must explicitly choose and confirm. Server decisions with an omitted automatic flag remain off. Also guarded an Access-editor return against a panel lock during refresh and prevented a lock during deferred routing from publishing a stale render.

Passed: `BRIDGE_GO="$PWD/.tools/go/bin/go" scripts/check.sh` (Go formatting, vet and full race suite; 27 panel/isolation tests; SDK types/build), `npm run test:e2e` (all three HTTP suites, including cross-panel invitation revocation), `npm run test:sdk` (91 tests), `npm run theme:check` (35 contrast checks), the guarded CLI lifecycle check and `git diff --check`. HTTP/CLI instances used only `--data-dir "$PWD/.local/ux-finish" --port 47461` with native providers disabled, and were stopped. New UI coverage uses the Node DOM stand-in; the earlier browser results above were not rerun. No installed application, native OS URL handler or real inference was used.

## 12 November 2026: visual consistency audit fixes

Resolved all 58 items in `.local/ui-audit.md` using shared type, spacing, radius, card, field-height and phone-target roles. Corrected action order, editor footer grouping, heading semantics/alignment, inheritance readability, danger hover, notice/toast treatment and the listed wording. The generated dark palette and existing focus, consent and close lifecycles remain intact.

Real Chrome 157 covered 1280 / 768 / 390 / 320 × 900px, with manual invitation pairing to `examples/server.mjs`, connection tests, both counts, long names, all editors, folder keyboard rings, selected/hover states, notices and empty/locked screens. No page or dialog overflow; phone targets meet 44px. Evidence and per-item resolutions remain under `.local/ui-polish-evidence/`. The runner used only `.local/ui-polish-data` on 47471, with native providers disabled; the sample used 47472. No installed instance, native launcher or real AI inference was used. Both servers and Chrome were stopped.

Passed: `BRIDGE_GO="$PWD/.tools/go/bin/go" scripts/check.sh` (SDK build output under `.local/`), all three `npm run test:e2e` suites on the required isolated directory/port, 27 UI/isolation tests, 91 SDK tests, 35 palette contrast checks, and `git diff --check`.


## 12 November 2026: request history opt-out

Added the default-on **Keep request history** switch and **Access → Request history** choices (global/keep/don’t keep). The Access draft uses its existing Save changes, revision checks and uncertain-save recovery. Turning history off confirms scoped deletion; the current global value stays readable below the selector at 320px. Requests explains global or connection opt-outs. Updated the panel’s privacy explanation and privacy/security/operations documentation.

Delivery decision: the unchanged signed protocol has no app acknowledgement. Successful signed result writes collect opted-out output; file results wait for all artifact bytes. Failed writes remain retryable, with a fixed one-minute fallback. Terminal records/files leave durable storage immediately, replay receipts remain, and live session context stays available.

Chrome Beta reviewed Settings, global confirmation, the Requests empty state, Access’s three choices, staging and saving an override at 320 × 900px, with no visible horizontal overflow. Only the disposable `.local/history-optout` runner on port 47481 was used, with native providers disabled; the fixture browser window and runner were closed. No inference or installed/dev-instance access was used. Validation includes Go/race lifecycle regressions, panel/isolation tests, all three encrypted HTTP suites (including unchanged SDK stream/lookup/retry opt-out checks), 91 SDK tests and 35 palette checks.

## 12 November 2026: owner defaults and connection overrides

Added global program/model/reasoning, maximum request time, history retention, output limit and idle connection expiry. Access keeps their overrides and connection notification mute in its existing revisioned draft and save recovery flow; cards mark overrides and show expiry. Shared number/unit fields validate inline, expose resolved seconds/bytes in plain words, and use bounded grid columns and existing focus/aria roles. Approval cards, invitations and Programs and models inherit the global program explicitly. Settings retains edited drafts through polling and offers reloading saved defaults.

Notifications keep the existing browser preference, with event choices and an optional synthesized chime. Start at login uses a tested desktop abstraction for recognized Linux/Windows installs; macOS/source/isolated instances hide it with an API explanation. No development test registers or removes an actual login item.

Validation covers settings precedence/bounds, legacy history and receipt migration, restart cleanup/artifacts, config output fallback, command output/timeout caps, a fake-clock one-hour request with separate approval waiting, renewed leases, open sessions and signed delivery, and panel controls/preferences. The Go race/check suite, 34 panel tests, all three encrypted HTTP suites, 91 SDK tests and 35 palette checks passed. HTTP suites used a fresh `.local/settings-binary` with only `.local/settings-pass` on 47491 and stopped their runners. Windows/Linux binaries cross-compile. Browser visual review at 320px could not run because both browser-control entry points reported an unavailable host; DOM checks and responsive CSS are covered, but rendered overflow/focus review remains unverified. No real inference, installed/dev instance access or OS startup changes were used.

## 12 November 2026: rendered settings and overrides audit

Reviewed the new owner defaults, connection overrides, invitations, approval inheritance, notification choices, startup setting and history confirmations in headless Chrome 157 at 1280, 768, 390 and 320px. Fixed 15 findings (3 high, 10 medium, 2 low): Access consent rows no longer collapse beneath Overrides; unit changes preserve amounts; connection model/reasoning can explicitly use program defaults and correctly describe inheritance. Reused the existing disclosures, switches, type/spacing tokens, borders and focus styles. Added wrapping inherited-value helpers, clearer expiry/history consequences and scoped confirmations, timed-history notes, unsaved draft protection, and an explanation for unavailable startup control. Full findings, before/after measurements and screenshots are in `.local/ui-audit-settings.md` and `.local/settings-ui-evidence/`; no findings were skipped.

Used only the native-disabled `.local/settings-ui` runner on 47492 and the manually invited sample app on 47493; only Test connection executed. Supported startup and model catalogs were browser fixtures; no login item or real inference was invoked. Rendering/interaction checks include all units, invalid values, save/reload, overrides/reset, empty/populated requests, confirmation cancellation/acceptance, keyboard order, accessibility descriptions and 44px phone targets. No horizontal overflow or page errors. Passed `scripts/check.sh` with the local Go toolchain (race tests and 39 panel/isolation checks), `npm run test:e2e` using the fresh `.local/settings-ui-binary`, 91 SDK tests, 35 theme contrast checks and `git diff --check`.
