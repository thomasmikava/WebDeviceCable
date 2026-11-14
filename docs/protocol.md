# Local protocol v2

WebDeviceCable retains the original `aib2.` invitation prefix, `ai-bridge-*` message kinds and `ai-bridge/v2/` cryptographic domain strings for protocol compatibility. Product naming does not change the signed bytes, pinned identities or SDK interface.

The listener defaults to `http://127.0.0.1:47321`. Exact Host and loopback remote-address checks apply. Browser Origin must match the invitation/pairing. Non-browser clients also supply Origin, but that header alone is not authentication. There is no relay. Both sides authenticate using separate ECDSA P-256 signing keys; prompts, invitation secrets, leases and responses travel inside an ephemeral encrypted channel.

Only `POST /v2/channel` and `POST /v2/request` are network integration routes. Direct resource calls and all `/v1/*` calls are rejected; there is no unsigned fallback. CORS preflight grants no authentication. Paired requests omit control credentials, refuse redirects and contain no bearer credential in their HTTP headers or URL. Local-network browser restrictions still apply.

## Trusted invitation and keys

The authenticated control panel creates a five-minute, single-use invitation binding app ID, display name, exact origin, app user, installation device, Restricted scope and consent. Its format is `aib2.` followed by unpadded base64url of UTF-8 JSON:

```json
{"v":2,"secret":"<64 lowercase hex>","endpoint":"http://127.0.0.1:47321","deviceId":"<64 hex>","publicKey":"<runner signing public key>","appId":"writer.example","appUser":"alice","origin":"https://writer.example","expiresAt":1790000000000}
```

Copy this bundle from the installed app's private launch link/control panel. It is the trusted transfer of the runner's public identity, not a key learned from an unauthenticated localhost endpoint. An attacker controlling that transfer can substitute their own identity. Do not put invitations in URLs, analytics or logs.

The SDK creates a distinct non-extractable WebCrypto signing key for each pairing. The runner retains its PKCS#8 private key in `identity.key` (0600 under a 0700 data directory on Unix). Only public keys are exchanged. Save the app's `ConnectionKeys` using account-scoped IndexedDB structured cloning if persistence is needed; JSON cannot preserve a CryptoKey. Do not ship a backend private key to browser code. Origin/page scripts can use the app's private key, even though it cannot be exported.

Legacy bearer pairings are revoked on upgrade. Missing/replaced runner keys invalidate existing pairings; corrupt keys stop startup. Key/address changes require a fresh trusted invitation. No endpoint or key is silently replaced by the SDK. Account/device changes require separate pairing. App account claims are locally confirmed, without independent upstream account attestation.

## Wire encodings and handshake

All binary fields use unpadded base64url. P-256 public keys use raw uncompressed SEC1 encoding (65 bytes). ECDSA signatures are SHA-256 over the **exact UTF-8 payload bytes**, encoded as fixed 32-byte `r` followed by 32-byte `s`, not ASN.1 DER. There is no JSON canonicalization step. A signed message is `{"payload":"<encoded exact bytes>","signature":"<encoded 64 bytes>"}`.

1. App generates an ephemeral ECDH P-256 key and a random 256-bit challenge (64 lowercase hex). Send JSON `{nonce, publicKey}` to `/v2/channel`, with the app's exact Origin.
2. Runner returns a signed payload with fields `{v:2, kind:"ai-bridge-channel", deviceId, publicKey, endpoint, origin, nonce, clientPublicKey, serverPublicKey, salt, sessionId, expiresAt}`. Here `publicKey` is its persistent signing key; `clientPublicKey`/`serverPublicKey` are the ephemeral ECDH public keys. `salt` is 32 random bytes; session ID is 64 random hex; expiry is Unix milliseconds, 15 minutes ahead.
3. Verify the signature against the invitation's pinned key and compare every identity/origin/endpoint/challenge/client-key binding before sending any secret. SDK checks expiry and allows no more than 16 minutes. A fresh challenge prevents handshake replay.
4. Derive the 32-byte ECDH shared secret. HKDF-SHA256 with the returned salt derives separate 32-byte AES-256-GCM keys; `info` is UTF-8 `ai-bridge/v2/<sessionId>/request` or `ai-bridge/v2/<sessionId>/response`. No signing key is reused for ECDH.

Every encrypted message is JSON `{sessionId, iv, data}`. IV is 12 fresh random bytes per message. GCM uses a 128-bit tag appended to ciphertext in `data`; AAD equals the corresponding HKDF info string. This protects both directions before and after pairing. The browser SDK decrypts locally, so the requesting website can read the content.

## Signed requests and claims

The plaintext of `/v2/request` is a signed message. Its exact signed payload contains:

```json
{"v":2,"kind":"ai-bridge-request","sessionId":"...","nonce":"<fresh 64 hex>","timestamp":1790000000000,"origin":"https://writer.example","pairingId":"...","publicKey":"<app signing key>","method":"POST","path":"/v2/jobs","lease":"...","body":"{\"kind\":\"connection-test\",\"idempotencyKey\":\"unique-request\"}"}
```

`body` is the logical JSON request encoded as a string, or empty. `lease` is optional. `authorization` is optional and accepts only the invitation's secret during `/v2/pairings/claim`; use an empty `pairingId` for that initial claim. The claim's body is `{"appId":"writer.example","appUser":"alice"}`. The verified response is `201 {"pairing":{...}}`; runner stores the app's public key bound to that pairing. No bearer credential is returned. Subsequent requests require that exact key, pairing ID and Origin.

Runner verifies the app signature, channel/origin binding, timestamp (within ±60 seconds) and a previously unused nonce before dispatch. Methods are GET, POST, PUT or DELETE. Logical paths cannot tunnel `/api`, contain queries/escapes/traversal, or call the transport itself. Invitation, pairing and lease checks remain mandatory within handlers. Every request, including heartbeats, close and cancellation, uses this transport.

Channel handshakes remain bounded to 128 KiB; encrypted request envelopes allow 4 MiB. Ordinary logical bodies retain 40 KiB. Only execution creation (`POST /v2/jobs`, `POST /v2/conversations`) and preflight (`POST /v2/admission`) allow 1,088 KiB, including at most 1 MiB tool definitions; their non-tool fields retain 40 KiB. Verified oversized logical requests return signed/encrypted 413 errors (`tool_set_too_large` or `request_too_large`). Unsigned failures remain security failures. Unknown logical body fields are rejected. Channels are in-memory, last 15 minutes, and accept at most 2,048 distinct request nonces; exhaustion fails closed. At most 256 channels (64 per origin) exist. The SDK renews before expiry. Restart clears channels, sessions and leases.

## Verified responses and streaming

A successfully processed outer request returns HTTP 200. Its body is an encrypted signed frame; use the **signed** inner `status`, never unsigned outer errors, as an application result. Invalid transport/authentication can return an unsigned outer error; the SDK treats it as failure and exposes no unverified job output.

Every signed response frame contains `{v:2, kind, sessionId, nonce, requestHash, origin, pairingId, method, path, sequence, previous, status, body}`. `requestHash` is lowercase hex SHA-256 of exact signed request bytes. The other request bindings must match. `sequence` starts at 0 per request. `previous` is empty initially, then lowercase hex SHA-256 of the previous frame's exact signed payload bytes. `body` is the logical JSON response string. Without negotiated fragmentation, non-stream responses have one frame, `kind:"response"`.

Streams use fetch-based POST SSE with `Content-Type: text/event-stream`; each frame is flushed immediately:

```text
event: bridge
data: {"sessionId":"...","iv":"...","data":"<encrypted signed frame>"}

```

Verify the GCM tag, runner signature, all request bindings, sequence and hash chain **before** delivering each chunk. `kind:"event"` carries a nonterminal full job snapshot. `kind:"end"` authenticates the final terminal snapshot, or has an empty body for a normal 30-second stream rollover. A terminal snapshot without an end frame is rejected. Missing/duplicated/reordered frames fail verification; a truncated connection is never counted as successful completion. The SDK reconnects network-interrupted streams with fresh nonces without resubmitting work, but stops immediately on verification failures. It filters duplicate snapshot revisions after reconnect. It never buffers the entire answer waiting for a final signature.

New clients offer `features:["fragments-v1"]` in `/v2/channel`; the signed hello echoes supported features. A large response/snapshot is UTF-8 encoded, split into at most 16 KiB byte pieces, and sent as `kind:"fragment"` with base64url `body`, followed by its original `response`/`event`/`end` frame with empty body. Every piece participates in the same signed sequence/hash chain and request binding. Fragmented ordinary responses also use SSE framing; the SDK returns a normal JSON response after verification/reassembly. Reassembled bodies are bounded to 52 MiB, individual outer frames to 256 KiB and incomplete SSE buffering to 512 KiB. Missing/reordered/replayed fragments or malformed UTF-8 fail closed. Authentication is unchanged for older clients; use the packaged SDK for larger results. Job progress currently flushes at up to four snapshots per second; available Codex deltas stream progressively within those snapshots.

## Capabilities and requesting-tab lease

The paths below are logical resources inside signed requests, never direct network calls:

- `GET /v2/capabilities`: protocol version 2, public pairing/scopes, provider flags/reasons, `connectionTest`, lease duration and remote-access status. Provider flags reflect installed native-client compatibility; `appTools`, `workspace`, `fileTransfer` and `maxTextBytes` describe the added interfaces. `actions` lists this pairing's configured-command grants and their availability.
- `POST /v2/lease`: create a lease for one requesting tab; response `201 {"lease":"...","expiresAt":"...","heartbeatSeconds":3}`.
- `PUT /v2/lease`: renew; put its ID in the signed `lease` field.
- `DELETE /v2/lease`: release and cancel its outstanding jobs. Idempotent; cannot release another pairing's lease.

Lease expiry is 12 seconds without renewal; a 100 ms worker tick cancels that tab's jobs, including those awaiting approval. Expired leases cannot be revived. Explicitly open a new lease and submit new work. Network loss, sleep and browser timer suspension may cancel work even while a tab remains open. The SDK releases on `pagehide` when possible; expiry is authoritative. Keep heartbeats in the requesting tab, not a service worker/backend. Closing the WebDeviceCable control panel has no bearing on app leases.

## Jobs

`POST /v2/jobs`, with an active signed `lease` field, creates:

```json
{"idempotencyKey":"a-unique-logical-request","kind":"connection-test","mode":"restricted","timeoutSeconds":120}
```

`202` returns the full job. Defaults: Restricted mode and the effective owner maximum request time (initially 600 seconds). `timeoutSeconds` accepts 1–3600 seconds; omission uses the connection override or global maximum, and larger requests within the hard ceiling are capped to that maximum. Execution time starts at `startedAt`; approval and queue waiting have a separate, fixed 10-minute bound. Configured commands also retain their `config.yaml` execution timeout. Keys: 8–128 characters. Automatic consent applies only within paired scopes. A manual job remains `awaiting_approval` until the local control session approves it.

Eligible local applications can submit native requests:

```json
{"idempotencyKey":"a-different-logical-request","kind":"text","provider":"codex","prompt":"Hello"}
```

`kind` also supports `image-generate` and `image-edit`, Codex-only. Optional `inputs` references completed uploads; `outputSchema` uses the validated subset in [native execution](native-execution.md). `command` jobs use the separate Commands mode below. `mode:"app-tools"` enables the explicit app tool dispatcher; `mode:"workspace"` additionally enables granted folder operations. Restricted remains the default. Provider execution cannot be unlocked by an app or a control toggle.

- `GET /v2/jobs/{id}`: current snapshot for this pairing only.
- `GET /v2/jobs/{id}/events`: fetch-based SSE inside the secure transport described above. The SDK exposes verified `event: job` snapshots with revision as `id`. Connections last at most 30 seconds. Reconnect with a fresh request nonce to receive current state; the SDK filters already-seen revisions. This is snapshot replay, not a lossless delta log. A stream alone does not renew the tab lease.
- `POST /v2/jobs/{id}/cancel`: cancel a nonterminal job. Idempotent.

Transitions: `awaiting_approval → queued → running → completed` for connection tests, successful commands and supported native requests. Ineligible/unavailable providers stop at `blocked`. Other terminal states: `failed`, `cancelled`, `timed_out`, `interrupted`. No terminal state is retried. Only explicitly configured/granted commands can perform general local actions. `actualProvider`/`actualModel` and usage are absent when no inference occurred; `billing` is `none` or `native_subscription` once native execution starts.

The idempotency key is scoped to its pairing. Same normalized request returns its existing job (`200`); different content returns `409 idempotency_conflict`. Another tab cannot adopt an active job's key/lease. Deleting terminal history retains a content-free key/request-hash/job-ID receipt; reusing that key returns `410 history_deleted`. History retains up to 500 jobs and 20 MiB of output/JSON content; admission evicts the oldest terminal job when full and returns `429 job_limit` if all are active. Minimal receipts live in 256 private, atomically written shards (16 MiB each), separate from job state. They never expire; full/corrupt receipt storage fails closed. A crash between receipt reservation and job persistence consumes the key and returns `410 history_deleted` rather than replaying execution. Prompts are removed on terminal transitions; request hashes are not exposed.

## Control surface

Only the private launch link bootstraps `POST /api/session`. The URL fragment is removed before API use. The response returns independent random `session` and `csrf` values. The panel stores only the 12-hour session ID in origin-scoped browser storage, sends it explicitly as `X-Bridge-Session`, and omits cookies. State changes additionally require the exact control Origin and `X-Bridge-CSRF`. GET `/api/session` recovers the CSRF value only with a valid session header. Browser storage is isolated by port, so separate local instances cannot overwrite or receive each other’s session credentials. Blocked storage requires reopening the private launch link after a refresh. Sessions last 12 hours and are lost on runner restart. The CLI alone uses its private control key to stop the runner. Static assets contain no private state. Root control keys and the runner signing key are private local files. Invitations are stored as SHA-256 verifiers; app pairings store public keys only.

The `/api` surface is internal to the bundled UI, not a stable integration API. App pairing keys cannot create invitations, approve jobs, change scopes or stop the runner.

## Configured commands

See [commands.md](commands.md) for YAML fields, startup-only web-editor policy, OS-account permissions and process lifecycle limitations. Native provider eligibility/isolation is enforced independently.

- `GET /v2/actions` returns `{actions:[{id,title,arguments,timeout,available}]}` for only this pairing's grants. Executable paths/definitions are omitted. Definition changes require renewed local grants; `available` becomes false.
- `POST /v2/jobs` accepts `{kind:"command",action:"greet",arguments:{message:"Hello"},idempotencyKey:"..."}`. Mode defaults to `commands`; explicitly labelling it Restricted is rejected. Provider/prompt/unknown input fields are rejected. Inputs/defaults are normalized before idempotency hashing, and erased on terminal transitions.
- Signed job snapshots include `actionTitle`, combined text `output`, optional `exitCode`, and `outputTruncated`. Nonzero exit/start failure produces `failed`; `exitCode:0` produces `completed`. Both are authenticated terminal frames. Execution has a profile timeout plus the existing overall approval/queue deadline. Process output can be buffered by the invoked program; the bridge flushes available snapshots progressively.

Only the authenticated local `/api` control surface can grant current action hashes, after explicit OS-account permission acknowledgement. These are exact `{id:hash}` mappings, preventing stale reviews from granting a newly changed definition. Existing Restricted pairings gain no command authority automatically. `/api/commands` creation and `/api/commands/{id}` update/delete additionally require the immutable startup configuration flag; there is no protocol v2 resource for editing commands or the policy file.

## Groups, sessions, diagnostics

These are inner signed/encrypted resource calls, never direct unsigned HTTP endpoints:

- `POST /v2/groups {groups:[{id,name,kind?,parentId?,operation?,hidden?,replaces?}]}` upserts up to 64 groups / 8 levels, rejecting missing parents/cycles. `kind:'ai'` permits native choices; omitted/`'command'` permits only granted commands. `GET /v2/groups` reads definitions. Hierarchy/kind changes invalidate affected work; declaration never grants access.
- `POST /v2/jobs` also accepts `kind:'action',group?,prompt?,arguments?` without provider/action. The owner chooses its route. A private execution plan is pinned at admission, with closest-parent inheritance and app default fallback.
- Native `text`, `image-generate` and `image-edit` requests may also omit `provider` when they supply a registered `group`, for both submission and `POST /v2/admission`. The group selects the owner's effective route; an explicit provider never overrides a supplied group. Malformed provider IDs are still rejected. Without a group, these kinds require an explicit program ID. Image routes must resolve to a compatible Codex program. Route resolution follows receipt lookup, so replaying an already consumed request recovers the original result even after its route changes; the resolved provider is not written into the caller's request or idempotency hash.
- `operation:'image'` identifies a native image stage for the local editor; it grants no image or command permission. `hidden:true` keeps legacy groups addressable for receipt recovery without showing them. New groups may list up to eight `replaces` IDs from this same pairing: on first declaration only, the owner’s existing effective custom route is copied. Repeated registration never overwrites a new local choice. Adding aliases does not cancel old work; changing an existing hierarchy retains the previous invalidation rules.
- Updated desktops advertise `routingVersion:2` and `routingConfigured` in capabilities. A route with `inheritOptions:true` inherits each unspecified native option separately, from app default through workflow to stage. `modelDefault:true` / `reasoningDefault:true` explicitly reset that field to the native client default. A program change resets incompatible inherited native options; an explicit compatible reasoning override survives model changes. Routes without `inheritOptions` retain their previous whole-target replacement semantics. Resolved plans contain concrete choices only and remain pinned at admission. Known unsupported model/effort choices fail before native inference. Saving local routing still requires its expected revision and ends active requests/sessions after confirmation.
- `POST /v2/jobs/lookup {idempotencyKey}` returns `{job:null|Job}` without running anything. Receipts survive erased job history; a consumed erased job yields `history_deleted`.
- `POST /v2/conversations {group?|action?|provider?|mode?}` requires a live lease and creates a context descriptor without starting a process. Codex and JSONL session commands retain a context; unsupported providers fail before opening it.
- `POST /v2/conversations/{id}/turns {idempotencyKey,prompt?,arguments?,timeoutSeconds?,inputs?,outputSchema?}` admits one serial, consented job with the pinned context/route. Repeated identical keys return the original job. Another tab/user, busy/closed context or changed route cannot continue it.
- `GET /v2/conversations/{id}` reads this pairing's descriptor. `DELETE` closes it and requires its original live lease. Descriptors omit private plans, lease and pairing keys. They are ephemeral execution contexts, not transferable provider session IDs.

Failures include stable `error:{code,category,message,retryable,raw?}`, retaining legacy `code/detail` fields on jobs. A configured program's code is mapped to a common category rather than guessed from arbitrary text. No error flag causes automatic replay. See [SDK error handling](sdk.md).

Jobs may include `turn` and actual `execution:{kind,name?,provider?,version?,versionSource?,model?,reasoning?}`. Metadata is attached only on actual process start; custom versions are marked configured. Local owner `PATCH /api/sharing` controls optional fields independently. They are omitted on every remote projection and active streams advance revisions when sharing changes. Plans, resolved selections and grant hashes are private. Output itself is not anonymized.

The owner-only control API also handles pending connection requests. A user-opened local popup verifies its opener and uses its authenticated session/CSRF to register the browser-observed origin. Pending requests appear in `/api/state`; invitation secrets do not. Any authenticated local panel can accept/reject once. The original popup retrieves the one-use invitation through the owner API and posts it only to its bound opener/origin/request ID. No website can directly add a queue entry, choose automatic consent or request command grants. Entries are memory-only, capped at 32 and expire in 5 minutes. This preserves the trusted first-pairing transfer assumption; it is not loopback software attestation. There is no silent key replacement for saved connections.


## Supplied inputs and output artifacts

All calls use the existing signed/encrypted transport. Query strings remain prohibited; artifact offsets are signed path segments.

- `POST /v2/inputs {name,mediaType,size,sha256}` reserves a tab-bound upload and returns its `id`. `PUT /v2/inputs/{id} {offset,data}` appends standard-base64 chunks up to 24 KiB at the exact next offset. Completion checks byte count, hash and transport limits; it does not parse document formats. `DELETE /v2/inputs/{id}` frees it. IDs from other tabs/pairings and incomplete inputs cannot be used.
- Jobs accept `inputs:[id]` and optional `outputSchema`. File bytes may have any media type. Built-in native adapters accept PNG/JPEG/WebP/GIF and bounded text/Markdown/JSON/CSV, and adapt PDF/DOC/DOCX/XLS/XLSX documents with explicit representation limits. File-enabled custom programs/commands receive the original bytes and decide their own formats. `nativeFileInputsVersion: 1` advertises the adapter file path; uploads have no extension allowlist. Supplied inputs are never app-provided local paths or remote URLs. An image edit requires an actual supplied image. File-enabled configured commands accept `inputs`; output schemas remain native-only.
- Jobs return `json`, `usage` and `artifacts:[{id,name,mediaType,size,sha256}]` when available. `GET /v2/jobs/{id}/artifacts/{artifact}/{offset}` returns `{id,offset,data,complete}`, standard-base64 chunks up to 24 KiB. The SDK checks ordering, size, terminal chunk and full-file hash. Native saved paths are never returned or opened.

File retention, schema subset and resource limits are in [native execution](native-execution.md). File transfers do not create provider calls or bypass job consent.


### Transport lifecycle

The SDK renews channels before 1,024 requests or expiry, with no mutation retries. Signed `DELETE /v2/channel/current` retires only the channel carrying that request, including before pairing. It does not revoke the pairing or other tabs; release the lease to cancel tab work. In-flight replies remain verifiable. Individual HTTP responses have a 10-second client timeout; stream connections have 40 seconds (runner rotates them within 30 seconds). Reading can reconnect; execution is never replayed automatically.

The CLI authenticates saved `runner.json` addresses through a domain-separated HMAC challenge at `POST /api/runtime` before transmitting its control key. This endpoint accepts only a fresh client-chosen 32-byte hex nonce and returns `{pid,proof}` bound to nonce, exact loopback origin and process ID. It does not expose the key, grant browser access or replace protocol-v2 runner signatures.


## Installed-app connection rendezvous

The SDK generates its non-extractable signing key before launching `webdevicecable://connect?requestId=<64hex>&origin=<exact-origin>&publicKey=<public-key>`. Only those three query fields are accepted. The local launcher authenticates the runner using its existing runtime challenge, creates a five-minute launch intent through the private control API, and opens the authenticated owner panel. A URL never grants permission.

`POST /v2/connect` accepts `{payload,signature}` signed by that requesting key. Its payload is `{requestId,origin,publicKey,nonce,timestamp,appId,name,appUser,ephemeralKey}`. The runner requires an unexpired matching launch intent, the actual browser Origin, a fresh 64-hex nonce (512 maximum), a timestamp within 60 seconds, a valid P-256 signature and stable app/account fields. It then creates the pending owner approval. No intent means no public pairing queue.

The signed response payload binds `{requestId,nonce,origin,clientPublicKey,clientEphemeralKey,publicKey,deviceId,endpoint,serverPublicKey,salt,encrypted}`. Derive the AES-GCM key exactly like a v2 channel with `session=requestId` and direction `launch`; both HKDF info and AEAD additional data use `ai-bridge/v2/<requestId>/launch`. Decrypted JSON is `{status,invitation}`. The SDK pins the responding identity throughout this flow and requires the approved invitation to match it. The server also binds that invitation to the original requesting signing key. Claim uses the existing mutually authenticated encrypted protocol. Initial trust comes from the installed application and owner consent; this is not OS attestation against malicious same-account software.

## Tools and permission grants

- Inline `tools:[{id,title,description,inputSchema,readOnly?,operationDetails?}]` belongs to session creation or a standalone job/preflight. There is no fixed tool-count cap in 0.2.1; limits are an 8 KiB/12-level schema subset per input schema and 1 MiB complete definitions. Capabilities omit `toolsPerExecution` when no count cap exists; an older peer may still advertise a positive cap. The desktop normalizes/sorts definitions and computes `toolSetHash`, binding it into request hashes and execution metadata. Reusing a request key with changed tools conflicts. Sessions store definitions once; standalone jobs store them only while live. Ordinary responses exclude definitions. Ended scopes retain hashes; aggregate live definitions are capped at 8 MiB before receipt reservation. Pairing-wide registration no longer exists.
- `GET /v2/permissions` returns folder descriptors; legacy tool/grant fields are empty. `POST /v2/permissions {workspace?:boolean}` requests folder review. App tools require application authorization rather than WDC grants. Folder paths and approvals remain local control operations.
- `toolInputSchemaVersion:1` advertises the independent tool-input subset documented in [SDK tools](sdk.md#tools-and-permission-apis), including optional/nullable fields and open objects. Output schemas retain the strict subset. Unsupported keywords fail before inference.
- `POST /v2/tool-calls/{id}/operation {id,title,version?}` attaches immutable application-resolved details to a claimed, live callback. Pairing/account/lease checks match claim/result. No arguments, permissions, tool identity or execution scope can be changed through this endpoint. Capabilities advertise `toolOperationDetailsVersion:1`. Reports persist with the job, bounded to 128 per job and included in aggregate retained-history budgeting; minimal replay receipts stay unchanged.
- `POST /v2/tool-calls/poll {requests:string[]}` returns this pairing/lease's approved `calls` plus `completed` notices for up to 256 observed request keys. Each call binds stable `id`, `jobId`, optional `conversationId`, `requestIdempotencyKey`, `toolSetHash`, name and arguments. Completion notices use retained jobs or owner-bound durable receipts after history removal; unknown request keys never authorize replay or handler execution. `GET /v2/tool-calls` remains a read-only callback inspection endpoint.
- Claim/result endpoints retain single-claim and identical duplicate-result semantics. Native startup and callbacks resolve the same immutable session/job snapshot. A tab/session/job closure never cancels another scope. Reconnection never resubmits inference or uncertain tool operations.
- Capabilities advertise `scopedToolsVersion:1` and `limits`. Relay capabilities are authenticated at `/relay/v1/capabilities`. Connector polls retain eight-packet batches with a combined 4 MiB packet-body ceiling and an 8 MiB bounded response. Protocol-v2 signatures, encryption, pairing identities, folder permissions and replay receipts remain intact across upgrade.

## Installed relay links

`webdevicecable://relay-connect?relayUrl=<exact-HTTPS-origin>&code=<64hex>` opens a relay invitation; `webdevicecable://settings?relayUrl=<origin>&connectionId=<64hex>` opens an existing local relay binding. Both accept exactly two fields, no credentials, fragment, path or extra query fields. HTTP is allowed only for an explicit numeric loopback development origin. The root-authenticated installed launcher records a memory-only five-minute intent and opens the authenticated panel; session/CSRF protected dismissal prevents reopening it on each poll. Reopening the same relay link is safe and never grants consent. Direct `connect` links keep their original requesting-key and replay rules.

Relay inspection and approval use the existing `/api/relay/inspect` and `/api/relay/connect` control routes. `/api/relay/decline` consumes the short-lived code through `/connector/v1/decline`. The relay retains a declined ticket until its normal expiry so the account backend can report a clear state. Pending/declined/expired ticket status follows the same project and account checks as connection status. Local approval does not grant automatic execution, commands or folders. `groupsDeclared` becomes true only after authenticated group registration; the panel waits for that step before opening the new connection’s workflow settings.

## AI program entries

`provider` in routes and requests identifies a stable local AI program. Existing `codex` and `claude` IDs select their editable default entries. Capabilities report per-entry `integration`, `isolation`, `streaming`, `sessions` and feature flags. `integration` and display names follow local sharing choices; executable/profile paths and diagnostic identities are local only. Execution metadata distinguishes `programId` from native `provider`; custom metadata uses `kind: custom`, optional `versionSource: program` and `billing: program_defined`.

Local control routes: `POST /api/programs`, `PUT/DELETE /api/programs/{id}`, `GET /api/programs/{id}/uses`, `POST /api/programs/{id}/check`, and explicit `POST /api/program-defaults/{codex|claude}` restoration. Used entries require `leaveUnavailable=true` on removal. `If-Match` protects editor saves with the configuration hash. `PUT /api/pairings/{id}/program-grants` accepts `{programs: {id: configHash}, commandPermissionsAccepted: true}` only in the authenticated local panel. No remote route can manage programs or grant their execution. Custom protocol details and limits are in [AI programs](ai-programs.md).

## Explicit file grants and text context (0.2.2)

`requestedPermissions.files` is a review suggestion, and `Pairing.files` is the approved grant (false for old state). `GET /v2/permissions` includes `files` and `automaticApprovals`. `POST /v2/permissions {files:true}` opens a desktop review under the active lease; the existing owner-scoped permission-status endpoint reports its decision. Only desktop control can approve it or change `PUT /api/pairings/{id}/files {allowed}`.

`POST /v2/contexts` uses the input metadata shape but reserves a text-only class (text/plain, <=128 KiB); ordered chunks and cleanup use `/v2/inputs/{id}`. Completion checks UTF-8 and NUL exclusion. Jobs and session turns accept `contexts:[id]` independently of `inputs`. Context never becomes a file manifest or tool-result file. File-bearing callback results use `{$wdc:"tool-result",result,inputs}` inside the ordinary result field. File access is enforced at desktop admission, transfer, callbacks and downloads. See [file permission lifecycle](file-permissions.md).
