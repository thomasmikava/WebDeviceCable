# Connection review and tool improvements (0.2.1)

Release candidate: 0.2.1. Browser SDK, relay SDK/server and desktop are released together. Existing 0.2.0 packages and release tags remain unchanged. The local-preview validation below is historical; see [0.2.1 release notes](release-0.2.1.md) for the coordinated release and remaining platform checks.

The preview also removes the former 32-tool count cap. Execution catalogs are bounded by their 1 MiB definition budget and existing schema/request/live-storage budgets. Authenticated desktop and relay capabilities omit `toolsPerExecution`; the SDK still reports an update requirement before submitting a larger catalog to an older peer that advertises a positive cap. SDK tests cover 300-tool jobs and sessions, callbacks at the end of the catalog, and oversized catalogs rejected by bytes rather than count.

## App API

Both transports accept the same optional suggestions when starting a new pairing:

```js
await BridgeClient.requestPairing({
  appId: 'rorido', name: 'Rorido', appUser: signedInUser.id,
  logoURL: new URL('/pwa-192x192.png', location.origin).href,
  requestedPermissions: { automaticApprovals: true },
});

await RelayClient.enroll({
  baseURL, apiKey, appUser: String(user.id),
  logoURL: 'https://admin.example.com/logo.svg',
  requestedPermissions: { automaticApprovals: true },
});
```

Suggestions appear on the authenticated desktop connection review. `automaticApprovals: true` checks **Approve requests automatically** initially. The user can uncheck it before Connect, or check it when the application did not request it. Omitting the permission retains **Ask each time**. Approval creates a restricted pairing with the reviewed policy; it grants no folders, commands or custom programs. Existing permissions and workflow routing continue to apply. The app remains responsible for authorization and confirmation within its own tool handlers.

The signed installed-app rendezvous binds suggestions to the initial request; the popup fallback carries the same fields from its verified opener origin. Relay enrollment stores suggestions with its expiring ticket. Desktop relay approval re-inspects and compares the entire displayed identity and suggestions. Changed terms require another review. No suggestions change an existing pairing or bypass local approval.

## Logos and owner changes

WDC fetches the proposed logo while showing the authenticated review. It sends no account identifiers, WDC credentials or browser cookies to the image host. Images are cached in the private desktop data directory. The panel displays authenticated cached bytes through local object URLs, rather than hot-linking the external server.

Pairing shows the app’s logo as identity, with no logo-editing controls or image-loading messages. If its logo cannot load, WDC quietly uses the placeholder. After connecting, **More options → Edit app** shows an editable display name, the current logo, an **Upload image** button and an image URL field with **Preview**. **Remove logo** clears the preview. **Save changes** applies the name and logo together to this connection; **Cancel** discards both. Saving stays disabled until the name or logo changes, and while an image is loading or a changed URL still needs previewing. Closing the dialog cancels image loading and discards the draft. Removal persists until the owner explicitly chooses another image. The application cannot replace an owner's chosen logo through execution requests. Display names are owner-only aliases: the original app name, account, pairing ID, permissions and active work are preserved, and the alias is omitted from app-facing responses. An empty `displayName` in the owner PATCH API restores the app-supplied name.

Supported formats: PNG, JPEG, GIF and a static SVG subset, maximum 256 KiB. Raster dimensions are at most 2048 × 2048. SVG excludes scripts, event attributes, CSS, external references and embedded documents. Remote fetching times out after five seconds, denies redirects/proxies and non-public addresses, and allows development loopback images only from the application's exact origin. Use the final image URL. Logo upload has its own bounded owner endpoint; ordinary logical request and tool budgets are unchanged.

Cache files use content hashes, are served only to authenticated owner sessions, and are capped at 32 MiB. Unreferenced previews older than 24 hours are pruned when adding images. Pairings retain only the selected logo hash. Optional fields load without changing existing identities, receipts or grants; no application database migration is needed.

## Local validation and rollout

Build `npm run package:sdk` for the `0.2.1` browser and relay tarballs. Build the desktop with `python3 scripts/package.py --target darwin/arm64 --out-dir dist/onboarding-preview`. Install the local archives in test consumers; do not commit filesystem dependency paths. Rorido supplies its own origin's `/pwa-192x192.png`; Kings requests automatic approval and uses its public admin `/logo.svg`, overridable with backend-only `BE_WDC_LOGO_URL` (use the registered loopback origin for a local image).

Test a fresh pairing, edit the default, connect, and verify the connection card. Then change/remove its logo and reload. Existing pairings retain their settings; a reconnect uses a new review. Local tests must use disposable WDC state for synthetic pairings and must never reset application databases. Package publication, relay deployment and any production rollout remain separate actions.

## Verified local candidate

`0.2.1-preview.1` is installed on this Mac, and Rorido's Vite server serves its matching SDK. The running desktop's authenticated HTTP state was checked against the original device and all seven pairings. All 20 original receipt files and both identity/admin key files are unchanged. A private backup contains the prior app and profiles. An earlier restart had inherited Codex's redirected home and opened a separate empty profile; the preview now explicitly uses the original profile. No databases were reset.

Validation passed: Go race tests (including relay suggestion/version checks), Go vet, 82 SDK tests, packaged ESM/CommonJS/bundler consumers, generated-output drift, direct/command/signed-launch HTTP suites, and the Docker relay suite using the actual installed consumer SDK archives. The relay suite covers a proposed automatic setting overridden to manual and an uploaded replacement logo, then continues through encrypted requests, isolation, restart and revocation. Rorido's 50 affected frontend tests and Kings' 18 WDC service tests pass, with scoped type/lint checks for both.

The web review was exercised in disposable state: Rorido's actual logo loaded, the checkbox was unchecked before approval, claiming retained manual approval, and the connected app's logo was removed through its menu. A final screenshot is in `dist/onboarding-preview/connection-review.png`. The native OS URL handoff was not automated (native UI control was unavailable); the signed launch rendezvous was verified over real HTTP. No provider inference was needed for these onboarding changes.

The pairing review is limited to app identity and editable permissions, followed by Decline/Connect. Logo customization is available only after connection.

The logo dialog was checked in disposable state for URL preview, invalid-image errors, Cancel discarding a draft, saved changes surviving restart, removal surviving reload, and clearing the URL without losing a valid preview. Controls are visible immediately; keyboard focus is retained after loading and removal. Its screenshot is in `dist/onboarding-preview/logo-settings.png`.


## Immediate request cards

The review explains optional access directly: “Connecting will not share your computer’s folders or give this app permission to run commands.” It does not ask the user to configure unused access.

Cards group the app logo, name and website together, put the automatic-approval choice in one selectable row, and keep account/application IDs under Connection details beside the action buttons. Background updates retain existing card elements, focus, disclosure state and the user’s checkbox choice.

Authenticated owner panels subscribe to `/api/connection-events` using their existing session header. A bounded notification wait wakes all panels when a launch, verified request, decision or claim arrives; the existing state refresh remains a recovery fallback. Direct installed-app intents immediately provide a loading card, before the signed SDK metadata arrives. Popup requests show a local loading card before registration returns. Relay requests use inline loading/review/error cards; they never wait for remote inspection before becoming visible. Manual relay review also shows loading feedback. Remote relay I/O releases the desktop’s shared lock, so it cannot delay other pairings or callbacks. Session locking, cancellation and server shutdown release notification waits. Revisions prevent older state responses from erasing a newer request.

Validation includes race-tested fanout to two panels, loading-intent replacement without duplicates, authenticated access, bounded waiters, cancellation, session locking, and a held relay inspection that does not block a new connection. The browser was checked for loading-to-ready replacement without reload and preserving an unchecked choice when another connection arrives. Direct/signed-launch/command HTTP suites and the Docker relay integration pass with the changed connector.


## Approval handoff and app appearance

Approved requests remain visible with the app name, selected logo, a **Connecting…** status and placeholder settings until the panel has loaded the matching paired app. Owner notifications carry the pairing ID and sanitized approval metadata through both browser and relay claims, including slow claims and events arriving before the full state response. Invitation credentials stay out of state/notification snapshots. Relay claim failure removes its pending card; approval records expire with their existing bounded lifetime. A notification immediately refreshes paired state so the finished card appears promptly.

Focused race tests cover the approval-to-claim handoff, notification/state ordering, slow and failed relay claims, rename persistence and validation, owner-only access, account isolation, app-facing alias privacy and preserving pending work. The combined dialog supports saving a name without changing the logo, or changing both together.

The updated local build passed the full Go race suite and vet, direct/command/signed-launch HTTP suites, Docker relay integration, syntax checks and generated SDK drift checks. Browser checks held the approved connection before claim, reloaded the pending card, completed the claim, saved a name-only edit, and cancelled simultaneous name/logo edits. The installed Mac app now serves the combined editor at its original port, with existing pairing, key and receipt data preserved. Screenshots: `dist/onboarding-preview/connection-connecting.png` and `dist/onboarding-preview/app-appearance-installed.png`.

The count-cap removal was verified with 300-tool admissions over both real HTTP transports and callbacks for tool 300 in standalone jobs and continuing sessions. An escaped 40-tool preflight exposed HTML escaping in relay request signing; relay bindings now use JSON-data encoding without HTML expansion, preserving signed bytes and fitting the existing 4 MiB encrypted envelope. Regression tests cover near-1 MiB logical catalogs containing HTML characters and Unicode line separators. Older count-capped peers are rejected before execution submission or callback registration.

## Tool input schemas and dispatched operation details (local 0.2.1 preview)

Tool inputs now use an independent bounded JSON Schema validator, leaving structured-output validation unchanged. Inputs support optional properties, nullable type arrays, bounded anyOf, generic or schema-constrained additional properties, constants and inclusive numeric/string/array bounds. Schema and transport byte/nesting budgets remain in force. Desktop and authenticated relay capabilities advertise toolInputSchemaVersion:1 and toolOperationDetailsVersion:1; SDK feature checks reject unsupported peers before execution creation or callback installation.

A marked dispatcher can await context.reportOperation({id,title,version}) after application resolution and before execution. The authenticated endpoint requires the live owning pairing/lease and a claimed callback, rejects conflicting details, and retains bounded job operation records. Activity/history show application labels and operation revisions. Returned callback results and unconfirmed interrupted calls are distinguished. Labels cannot grant permissions, change tools, reroute callbacks or alter execution hashes. Minimal durable replay receipts remain unchanged; ordinary history retention includes the operation-detail bytes.

Rorido chat (hosted and local) and external MCP now share list_domains, list_tools_by_domain and execute_tool. Definitions supplied to WDC total about 1.8 KiB. Backend catalogs/handlers are captured per turn; domain pagination includes operation schemas/versions and fits a 24 KiB encoded callback result. Catalog changes preserve native memory when the three interface definitions are unchanged. Rule/syllabus/quiz actions remain tool-free. Browser callbacks verify issued execution context, resolve display details through the authenticated backend and confirm their WDC report before dispatch. Rejected version/argument proposals return actionable results without executing. Existing optimistic revisions, current-user authorization, MCP scopes and callback deduplication remain in force.

Validation:
- Full Go race suite and vet; focused race checks after the final tool-input validator adjustment.
- SDK type/build/generated-output drift, 88 SDK tests, both npm archives and CommonJS/ESM/bundler consumer checks.
- Direct/relay HTTP creation, ordinary-request limits, escaped catalogs, 30/3 and 300-tool regression fixtures.
- Real Codex and Claude discovery, JSON argument objects, resolved operation details, changed backend versions and continuing native memory through both direct and relay transports.
- Rorido backend/MCP/chat suite (102 tests plus a separately exercised native fixture), focused recovery tests, frontend chat/integration checks, scoped TypeScript and Biome checks. Dedicated test state only; no application database migration or reset.
- Actual Rorido catalog and callback routing through real Codex and Claude; installed preview CLI also exercised with Claude and disposable state. Native answers persist at zero credits and never instantiate hosted inference.
- Operation-label/version UI checked in a disposable profile. Screenshot: dist/onboarding-preview/operation-details-validation.png. The real profile was not populated with QA jobs.

The installed preview remains on port 47321 and Rorido on 4310/API 3984. Upgrade preserved nine stored pairings, identity/admin keys and every receipt. Local SDK tarballs/lockfile integrity are refreshed without filesystem dependency pins. Nothing was published or deployed. Hosted OpenAI/Gemini provider calls were validated with application fixtures, not live paid-provider calls.

Final recovery checks: stale-version/argument proposals return structured errors to the model before any action or operation report. Backend ownership/expired-turn failures remain hard failures. Focused backend recovery tests and the installed-CLI Claude integration passed after this adjustment. The first concurrent frontend rerun hit the existing global cleanup hook's 10-second timeout while importing the whole test application during other local compilation. Running the same 51 tests with one worker passed; no assertions, retries or timeout limits were changed.

## Review fixes included in 0.2.1

Claude launch arguments are independent of the number of tools: only the execution's private WDC MCP server is allowed, while startup and every callback still validate the exact snapshot. A 4,000-tool catalog passes launch/isolation regression checks on macOS and Linux. Numeric bounds outside the existing 256-character / exponent-magnitude 1,000,000 validator limits fail at admission; comparison failures propagate, and fractional length bounds cannot pass through floating-point rounding.

Validation after these fixes passed: full Go race suite and vet; 88 SDK tests, TypeScript and generated-output checks; Linux catalog/schema regressions; and real Claude discovery/callback/operation-detail/continuing-context tests through direct and relay connections.
