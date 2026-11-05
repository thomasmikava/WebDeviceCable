# Using WebDeviceCable

Use `@webdevicecable/sdk` in your frontend, import its version-pinned native ESM files by URL, or self-host the three browser modules. See [installation](integrate.md#1-install-and-include-the-sdk) and [npm distribution](npm.md); registry/CDN examples require the corresponding version to be published. Local npm tarballs remain available before publication. ESM, CommonJS frontend tooling and TypeScript are supported, with no runtime npm dependencies. Use one `BridgeClient` per requesting browser tab. Your app keeps its own accounts, authorization and output validation.

## Quickstart

Start with [Integrate WDC](integrate.md) for the complete browser setup. `ConnectionStore({appId,appUser})` saves/loads a non-extractable pairing using account-scoped IndexedDB; it never stores a lease. Pass the same `account` to `new BridgeClient({connection,account,groups})` so `connect()` verifies the authenticated account before opening a lease or registering groups. Call `close()` and clear the store on logout/account changes; revoked/lost keys require explicit re-pairing. The bundled SDK and local npm archive have no runtime dependencies.

## Pair from a button

```ts
// Call directly from a click handler so the browser can open the approval window.
const { connection } = await BridgeClient.requestPairing({
  appId: 'writer.example', name: 'Writer', appUser: signedInUser.id,
  logoURL: 'https://writer.example/logo.png',
  requestedPermissions: { automaticApprovals: true },
});
```

The optional logo and requested permission fields require the [unpublished 0.2.1 local preview](connection-review.md). The user can edit the proposed approval setting before Connect. The logo appears as app identity; replacement, upload and removal are available in the connected app’s settings. Omitting `automaticApprovals` retains Ask each time. Existing pairings are unchanged.

The packaged application registers `webdevicecable://connect`. The SDK opens a small connection window, offers to launch WDC and waits for explicit approval on **Connections**. The launch URL carries a public key, request ID and origin, never an invitation or control secret. The installed launcher enables a bounded rendezvous; the browser then proves possession of that key using its actual Origin. Invitation delivery is signed and encrypted, and the invitation is bound to the requesting key. Requests expire after five minutes.

Keep manual `BridgeClient.pair({invitation, appId, appUser})` for browser/network/installation restrictions. `requestPairing({launchInstalled:false,...})` retains the existing authenticated-panel popup flow. Neither path silently replaces a saved runner identity.

First pairing still requires trust in the installed local panel. This does not provide OS attestation against hostile local software impersonating its loopback address. Existing connections retain pinned keys; the SDK never replaces a saved identity silently. `ConnectionKeys` contains a non-extractable CryptoKey: use account-scoped IndexedDB structured cloning, not JSON/localStorage, if saving it. See [protocol](protocol.md).

## Groups, with optional subgroups

```ts
const bridge = new BridgeClient({
  connection,
  groups: [
    { id: 'ai', name: 'AI', kind: 'ai', children: [
      { id: 'chat', name: 'Chat', kind: 'ai' },
      { id: 'courses', name: 'Course creation', kind: 'ai' },
    ] },
    { id: 'export', name: 'Export' }, // standalone, commands only
  ],
});
await bridge.connect();
```

The website owns group IDs, names and kinds. `kind: 'ai'` gives a group AI choices in WDC; omitted/`'command'` means commands only, enforced by the runner. Every group can stand alone; children are optional. AI groups also allow explicitly granted custom programs. Nested IDs flatten to `ai.chat`, `ai.courses`; a flat list with `parentId` is also supported. Each child's kind is explicit; it does not inherit its parent's kind. IDs are stable ASCII identifiers, up to 64 characters; there are at most 64 groups and 8 levels.

In Connections → app → **Preferences**, the user chooses an app default and optional overrides at any group/subgroup. The closest override wins, then the app default. Selections replace the whole profile, including model/reasoning preferences. An ordinary group cannot inherit a native AI target: it needs a command selection on itself or an ancestor. Codex models and reasoning choices come from the installed client catalog. Saved preferences are distinct from actual execution metadata. Native eligibility and compatibility are reported by `capabilities()`.

`registerGroups()` upserts definitions without deleting unrelated groups; only the local owner changes routing. Declaring groups never grants commands. Changing hierarchy or kind ends affected grouped work. Routing changes end live routed work/sessions; definition/grant changes require fresh command permission. Idempotency receipts are checked before route resolution so retries cannot execute a newly selected program.

```ts
const result = await bridge.run({ group: 'export', arguments: { format: 'txt' } });
// Or submit + stream when your UI needs progress:
const job = await bridge.submit({
  kind: 'action', group: 'ai.chat', prompt: 'Hello',
  idempotencyKey: crypto.randomUUID(),
});
for await (const snapshot of bridge.stream(job.id)) render(snapshot);
```

`run()` connects, generates an idempotency key, waits through consent/queue/streaming and returns a completed job. A failed terminal job throws `BridgeJobError` with the job attached. Supplying explicit `provider` or `action` to `run` uses the lower-level direct route; `kind:'action'` uses the user's local choice. Arbitrary argv/executables never come from the website.

## Continuing a context

```ts
const chat = bridge.session({ group: 'ai.chat' });
try {
  const first = await chat.run('First message');
  for await (const update of chat.stream('Follow up')) render(update.output);
} finally {
  await chat.close();
}
```

A session starts lazily with its first message. `send()` returns the job immediately; `stream()` yields snapshots; `run()` waits for completion. All messages in a session use the same owned program/context and private working directory. Each turn still needs consent unless automatic execution was explicitly enabled. Only one turn runs at a time. Independent sessions have separate state and IDs. Codex sessions keep one ephemeral native thread; custom programs use the opt-in JSONL interface in [commands](commands.md). Claude sessions keep one owned `claude -p` process and a random in-memory native session; no saved history is resumed. They require a compatible personal subscription login and verified isolation for the requested mode.

The SDK calls are identical for both native clients. Prefer declaring an AI group and letting the user choose Codex or Claude Code in Connections → Default program. To select Claude explicitly, use `bridge.session({provider:'claude'})` or a text request with `provider:'claude'`; inspect `capabilities()` first. Supplied images are inputs to either client; image generation/editing always requires Codex.

Sessions are bound to the app user, pairing and requesting tab lease. Close, cancellation, failed turns, an explicitly configured command idle expiry, revocation, pause, route/grant changes and deadlines terminate them. Runner restart interrupts them; no context is silently recreated. An idle session counts toward process capacity (4 globally, plus the command's limit). `getSession(id)` reads its descriptor without sending work. `session({id})` can reopen a handle only within the same still-connected tab/lease; it cannot transfer a context to another tab.

`bridge.close()` releases this tab's jobs and sessions. Browser `pagehide` does this automatically, with lease expiry as the fallback. A stream AbortSignal stops observing; use `bridge.cancel(job.id)` to cancel execution. Closing the WDC control panel does not end requesting websites' sessions.

## Errors and private metadata

Errors expose `{code, category, message, retryable, raw?}`. Categories include validation, authentication, permission, unavailable, rate_limit, context_limit, cancelled, timeout, disconnected, execution, protocol, conflict, interrupted, internal, transport and security. `retryable` is advice for a deliberate next request, never an automatic replay. Unknown custom error codes become `command_failed`; their details stay in bounded diagnostics. Signed failures are distinct from unverified transport responses.

An ambiguous submit throws `submission_unknown` with its `idempotencyKey`. Use `lookup(key)` (read-only, no execution), or retry the exact original input/key. A session blocks different messages until `recover()` finds the previous job or you explicitly retry its exact key. A null lookup is not proof a delayed submission can never arrive. Do not generate a replacement request automatically. Stream reconnection reads snapshots without submitting work again.

Jobs include `execution` only after the process actually starts. Optional fields report a human-readable program name, known version, actual model/reasoning and provider. Custom versions are owner-configured, explicitly marked `versionSource:'configured'`; no version is invented or automatically probed. Requested native preferences are private plans, not actual model metadata. A conversation's turn number is immutable on each job.

WDC Settings independently controls names, versions, model/reasoning and diagnostics. Disabled optional metadata fields are omitted completely, including from running SSE snapshots, GETs, lookups and submissions. Selected routes and grant hashes never leave the owner panel. Raw diagnostics are off by default, bounded to 4 KiB, control-cleaned and secret-pattern-redacted. Redaction cannot identify every possible secret; program output/arguments can reveal private details independently of these metadata switches. Previously received information cannot be withdrawn. Errors do not echo private executable paths unless diagnostic sharing is enabled.


## JSON, supplied files and images

```ts
const input = await bridge.upload(file, file.name); // Unchanged file bytes; target support varies.
const job = await bridge.run({
  group: 'ai.chat', prompt: 'Summarize the supplied document.', inputs: [input.id],
  outputSchema: {
    type: 'object', properties: {summary: {type: 'string'}},
    required: ['summary'], additionalProperties: false,
  },
});
render(job.json); // independently schema-checked by WDC
await bridge.removeInput(input.id);

const picture = await bridge.run({
  kind: 'image-generate', provider: 'codex', prompt: 'A yellow circle on white.',
});
const png = await bridge.artifact(picture); // Blob; verified encrypted chunks + hash
// Editing: upload the PNG, then kind:'image-edit', inputs:[reference.id].
```

Session messages accept the same `inputs` and `outputSchema` fields. Uploads are tab-bound and memory-only; keep them until the approved request starts. They are removed on tab-lease expiry/revocation. A failed/ambiguous upload is discarded; upload again explicitly rather than replaying a partial chunk. Outputs remain pairing-scoped until history deletion. Do not log file contents or treat model text as trusted HTML. `job.billing` is `native_subscription` only once native execution starts, otherwise `none`; usage is reported only when supplied by the native client. See [supported formats, schema subset, sizes and native-cache behavior](native-execution.md).


## Tools and permission APIs

`session({tools})`, standalone `submit({tools})`/`run({tools})`, and `preflight({tools})` accept execution-scoped tools. No constructor tools or `registerTools()` API exists in 0.2.0. Omission means no app tools. Supplying tools infers App tools mode; explicit Restricted mode with tools is rejected. Workspace and command access require explicit modes.

A session snapshots definitions and handler references at construction, opens lazily, and retains its snapshot across messages. Message options cannot replace tools. To change the registered native definitions, create a new native session and supply the application's saved history/summary. Other sessions and active jobs retain their snapshots. Standalone submission captures its own definitions and callbacks; preflight validates only and never installs handlers or creates execution state.

Definitions contain `id`, `title`, `description`, `inputSchema`, optional `readOnly`, `operationDetails` and `execute(args,context)`. Callback context contains stable callback `idempotencyKey`, `jobId`, optional `conversationId`, `requestIdempotencyKey`, `toolSetHash` and `signal`. The SDK installs handlers before submission and routes by execution, so overlapping IDs are safe. Polling runs only for pending tool-enabled executions, with at most four callbacks concurrently. Completion notices are owner-scoped and survive history deletion through receipts. Terminal execution/session/client closure releases handlers and aborts signals. Claims and uncertain callbacks are never automatically replayed.

Tool inputs have an independent, portable schema contract (`toolInputSchemaVersion:1`): root `type:"object"`; optional properties; `additionalProperties` omitted/true/false or a schema; single types or type arrays (including null); `anyOf` (1–16 branches); `enum` (1–64 values), `const`, typed arrays, descriptions/titles, inclusive numeric bounds, string-length and array-length bounds. Object properties/required keys are bounded to 64, schema depth to 12 and bytes to 8 KiB. Numeric bounds must be finite and use at most 256 literal characters with an exponent magnitude of at most 1,000,000; unsupported representations fail at admission. Length bounds must be nonnegative safe integers, checked without rounding fractional values. Empty nested schemas are unconstrained. References, remote resolution, patterns and other keywords are rejected before inference. Output schemas keep their existing stricter contract. Arguments are checked against the captured input schema before dispatch.

A dispatcher can declare `operationDetails:true` and call `await context.reportOperation({id,title,version?})` after resolving/validating the operation in its own registry and before executing it. This authenticated report is scoped to the claimed callback and live owning lease/job; repeating identical details is idempotent, changing them conflicts. Labels and revisions are application-supplied descriptions, never permissions or callback-routing inputs. SDK checks require `toolOperationDetailsVersion:1` before submitting marked tools. Report text is bounded (ID/version 128 UTF-8 bytes, title 200), with no control characters. Jobs retain up to 128 reports in `toolOperations`, shown in local activity/history and receipt lookup while the job record exists. `returned` means a callback result was delivered, not that its application operation succeeded. Calls interrupted before delivery become `unconfirmed`; no inference or operation is replayed. Clearing history retains only the existing minimal replay receipts, without operation labels.

Applications can implement domain discovery with three stable tools. The backend catalog and revisions remain application-owned. Updating that backend catalog does not change the native tool snapshot or require a session replacement; each application must bind discovered revisions to the handlers it executes.

A conflicting submission never removes the original execution's callbacks. Matching retries share their snapshot until all attempts are definitively rejected, or the accepted execution ends; uncertain outcomes retain callbacks for receipt recovery. A session creation response from a closed or replaced client connection is rejected before installing its snapshot.

`requestPermissions({workspace:true})` requests folder access in WDC. Existing folder grants complete immediately. Use `{wait:false}` and `permissionRequest(id)` for asynchronous review. `permissions()` returns folder IDs/labels/access levels, never paths; legacy metadata fields are empty. App-tool grant requests are rejected. Applications retain domain authorization, confirmation and mutation deduplication responsibilities. `readOnly` is descriptive metadata.

Desktop capabilities and authenticated relay `GET /relay/v1/capabilities` advertise `scopedToolsVersion:1`, `toolInputSchemaVersion:1`, `toolOperationDetailsVersion:1` and explicit byte/schema budgets. Version 0.2.1 removes the fixed tool-count cap; current capabilities omit `toolsPerExecution`. New SDKs return a clear desktop/relay update requirement if a proposed catalog exceeds an older peer's advertised positive count cap, before submission or callback registration. Definition, schema and ordinary-request byte checks remain local SDK validation errors where possible. See [execution limits and example](integrate.md#optional-app-tools-and-folders).

Text output defaults to 1 MiB, configured by `bridge.maxTextBytes` (4 KiB–2 MiB). Exceeding it produces `output_limit` with retained partial output. Negotiated `fragments-v1` transparently handles larger signed/encrypted snapshots, including Unicode boundaries; `stream()` continues yielding complete verified replacement snapshots. Pairing identities do not change. Native sessions follow the tab lease without a default idle timeout; owner-defined command session timeouts remain available.
