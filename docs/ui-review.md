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
- Each connection leads with its app name and exact website origin. **Default program** opens **Program settings**; **Request approval** is visible beside it. Empty permission summaries and the account UUID are absent from the card. **Connection details** in the menu retains technical identity information; folder and command access remain available there.
- **Run automatically** still requires explicit confirmation. Cancelling or dismissing that prompt keeps the original policy. Routing labels describe task-specific choices and their effective program instead of override/inheritance terminology.
- All static and dynamically created dialogs dismiss on a backdrop click. Clicks inside blank modal padding and drags starting inside the modal do not dismiss it. Escape, close buttons, cancelled confirmations and keyboard focus restoration remain supported.
- Empty states use short, direct copy. Connection requests retain their exact origin, optional account details and explicit Connect/Decline controls. History remains expandable, with approval requests presented first.

Browser verification uses disposable state in `.local/card-ui-qa`, not a real app connection. It covers all four screens at 320px, the Connections layout at 390px and desktop, static/dynamic modal dismissal, cancelled automatic consent, focus return, and a completed local connection test. No AI inference requests were made for this redesign. Chrome on macOS was exercised; other browser engines and native Windows/Linux UI remain external runtime checks.

## 26 October: folder access and selection

- Folder selection is a view inside the existing access dialog, without a second backdrop or stacked modal. Back preserves the draft; closing or cancelling does not change grants.
- Home/Up controls, clickable breadcrumbs, folder icons and search distinguish navigating from selecting. The action names the folder being added. Its location trail appears before selection, and its full canonical path is visible in the access card before saving. Additional folders start near the last selection.
- Entering a path is optional. The entered location must open successfully before it can be added; loading, failed and edited paths cannot select a stale folder. Empty folders remain selectable, duplicates are explained, and the bounded listing reports truncation.
- Protected locations are visibly unavailable for sharing, using the same server-side boundary as saved grants. Owner-only folder listings still require the authenticated control session; browsing never grants access.
- Folder cards show read/edit access and optional edit automation. New folders remain read-only. Unsaved changes are explicit, and Save stays disabled when settings match their original values.

Verification uses a local fixture app and folders under `.local/folder-ui-qa`. It covers navigation, search, invalid paths, empty folders, duplicate selection, staged grants, edit defaults and explicit saving. No real application folder grants or AI requests are needed for these checks.
