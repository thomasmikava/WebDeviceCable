# Self-hosted relay and backend npm client

The optional relay lets a project backend use a user's desktop without a browser-to-local connection. The desktop still listens only on loopback. It initiates HTTPS long polling to the project's relay; local administration is never forwarded. Direct browser pairings continue to work concurrently.

Every deployment follows the same setup: start the relay with an operator secret, register each website once, then use its returned project key with `RelayClient`. This applies to one website or many; there is no implicit default project.

## Local Docker deployment

Build from this repository (Docker downloads the Go build toolchain):

```sh
mkdir -p .local
chmod 700 .local
openssl rand -hex 32 > .local/relay-admin.key
chmod 444 .local/relay-admin.key
WDC_ADMIN_KEY_FILE="$PWD/.local/relay-admin.key" docker compose -f compose.relay.yml up -d --build
```

Compose binds `127.0.0.1:47323`, mounts the operator secret read-only and persists SQLite in its own volume. The process runs as UID/GID 10001. The host secret directory is private (`0700`); its key file is readable inside the non-root container (`0444`). Keep that parent directory private when using file-backed Compose secrets on Linux.

`/healthz` reports process health; `/readyz` checks SQLite. One process owns each database, enforced by a file lock. Back up the volume privately; it contains app-side signing keys. Provider credentials and the desktop private key never leave the desktop.

The relay requires `WDC_ADMIN_KEY_FILE` (recommended) or `WDC_ADMIN_KEY`, with at least 32 characters. Flags are `--data-dir` and `--listen`. Project identities and credentials are managed through the API, never through relay environment settings. Rotate the operator secret by updating it and restarting the relay; use the project API below to rotate individual website keys.

For a server, terminate HTTPS at a reverse proxy, keep port 8080 private, disable response buffering, allow requests up to 24 MiB and stream duration up to 10 minutes. Do not publish the desktop listener. The connector refuses redirects and HTTP except a numeric loopback address explicitly entered for local development. Keep clocks synchronized.

## Register a project

Register every website once, including a private relay used only by Kings. Registration works at runtime without restarting the relay. Both CommonJS and ESM entries export `RelayAdminClient`:

```js
import { RelayAdminClient } from '@webdevicecable/relay';
const admin = new RelayAdminClient({
  baseURL: process.env.WDC_RELAY_URL,
  adminKey: process.env.WDC_ADMIN_KEY,
});
const { project, apiKey } = await admin.createProject({
  appId: 'kings-international', name: 'Kings International', origin: 'https://admin.example.com',
});
// Store apiKey in this website's backend secret store. It is returned only once.
// Use project.appId and project.origin as the website's WDC identity.
```

Keep the admin secret exclusively in the operator's provisioning service; each website receives only its project key. Restrict `/relay/admin/` at the reverse proxy to your provisioning network when possible. Registration is operator authenticated. A hosting product can automate it after applying its own signup, domain verification and abuse controls.

Each registered project has an immutable app ID, display name and exact origin, a separate credential, accounts and desktop pairings. The website's backend calls the relay. Each project requires its own desktop approval. Identical account IDs on different projects do not share a connection, job, file, session or local grant.

| Operator route | Behavior |
|---|---|
| `POST /relay/admin/v1/projects` | Register `{appId, name, origin}`; return `{project, apiKey}` once |
| `GET /relay/admin/v1/projects` | List project metadata, including revoked projects; no credentials |
| `GET /relay/admin/v1/projects/{id}` | Read project metadata |
| `POST /relay/admin/v1/projects/{id}/rotate-key` | Return a new key once; invalidate the old key for subsequent requests |
| `DELETE /relay/admin/v1/projects/{id}` | Irreversibly revoke the project, pending codes and all connections |

All operator routes require `Authorization: Bearer <admin key>`. The npm client exposes `listProjects()`, `getProject({projectId})`, `rotateProjectKey({projectId})` and `revokeProject({projectId})`. Project credentials cannot manage projects; the admin credential cannot submit inference. Execution routes identify the project from its credential and enforce connection ownership. `X-WDC-Account` adds an account check within that project.

Creation returns `409 app_id_exists` for a duplicate app ID and never replaces its key. If a create response is lost, list projects, locate that app ID, then rotate its key. If a rotation response is lost, rotate again. Only credential hashes are stored. Rotation preserves desktop pairings and receipts; a call already authenticated may finish. Revocation cancels active exchanges, denies connector polling and stops that project's desktop work while other projects continue. Receipts and metadata remain to prevent replay. App IDs stay reserved after revocation. To change project identity, register a new app ID and obtain fresh desktop consent.

Stored project records and their scoped pairings/codes continue to load using only the admin secret, including records created by the earlier configuration-based setup. Obsolete project environment settings are rejected with instructions to use registration. State predating project ownership is rejected without mutation: preserve its data directory, start with a new directory/volume, register the projects and pair again. Never reuse unscoped connections under a different identity.

Projects share operator trust, storage and capacity limits. Use separate deployments where operators or storage must be isolated. One relay process per database remains required.

## Production artifacts

`.github/workflows/publish-relay.yml` is a manual, protected-environment workflow for publishing the Docker relay to GHCR with provenance and an SBOM. Deploy its reported image digest, rather than a mutable tag. The initial GHCR package must be made publicly readable before another registry can import it. The image runs on linux/amd64; local Docker builds remain available for development.

`.github/workflows/publish-npm.yml` separately prepares and publishes both npm packages. It defaults to a dry run and retains the verified tarballs for review. See the [npm release runbook](npm.md) for first publication, scope ownership, the `release` GitHub Environment and each package's trusted publisher. Package generation and ordinary CI do not publish anything. Increment the repository version before a subsequent npm release.

A production host needs persistent local/block storage for SQLite, HTTPS termination, a privately mounted operator secret, a restart supervisor and consistent backups. Keep the management API private while exposing connector and execution routes over HTTPS. Proxy requests need up to 24 MiB, immediate stream flushing and at least 650 seconds for a provider-format response. Kings' Azure infrastructure implements these requirements on one dedicated VM with an ext4 managed disk and Caddy; its deployment workflow pins image digests and provisions Kings through the ordinary registration API. Native execution remains on the user's desktop.

Do not restore an older database and resume its old connections automatically: the snapshot may predate consumed inference. Keep the restore private, fence old connections/codes, preserve receipts, reconcile uncertain work and require new pairings before reopening traffic. Image rollback must preserve the current database.

The relay image owns database maintenance; integrations do not need to interpret its SQLite records:

```sh
wdc-relay backup --data-dir /data --backup-dir /backups --retain 7
wdc-relay fence-restored --data-dir /data --backup-dir /backups
```

Mount the existing data and private backup directories into a disposable instance of the same reviewed relay image. Maintenance needs filesystem access, no operator key or network access. `backup` includes committed WAL data and retains only WDC snapshots. Stop the server before `fence-restored`: it requires the same exclusive directory lock as the server, creates a forensic snapshot, revokes restored connections and pending pairing codes, and preserves projects and consumed receipts. Reconcile uncertain operations and require fresh pairings before reopening traffic. Missing state is rejected rather than replaced by an empty database.

One relay may host QA and production as separate projects with distinct app IDs, exact website origins and credentials. Keep each environment's project key in its own secret store. Shared-host maintenance affects both environments; project isolation does not create separate availability domains.

## Independent backend npm package

`npm run package:sdk` creates two independent npm archives in `dist/`: `@webdevicecable/sdk` for direct browser connections, and `@webdevicecable/relay` for Node 22+ backends. Neither requires a sibling checkout or runtime dependencies. Building artifacts does not publish to npm.

The backend package supports ESM (`import { RelayClient } from '@webdevicecable/relay'`) and CommonJS (`const { RelayClient } = require('@webdevicecable/relay')`), with TypeScript declarations for both. Both entries share the same runtime and error classes. When vendoring it as a Yarn workspace, include the workspace files in production images alongside `node_modules`; Yarn's workspace link does not contain those files.

After the corresponding version is published, install `npm install --save-exact @webdevicecable/relay@0.1.1`. Before publication, or for offline installation, use `npm install /path/to/webdevicecable-relay-0.1.1.tgz`. The backend package is independent of the browser package and ships no server binary.

```js
import { RelayClient, relayConnectURL, relaySettingsURL } from '@webdevicecable/relay';
// WDC_PROJECT_KEY is the key returned by createProject, stored in this backend.
const project = { baseURL: process.env.WDC_RELAY_URL, apiKey: process.env.WDC_PROJECT_KEY };
// Derive appUser from authentication, never a request body. Save connectionId per owner.
const enrollment = await RelayClient.enroll({ ...project, appUser: String(authenticatedUser.id) });
// Return only the short-lived launch URL to this authenticated account.
const launchURL = relayConnectURL({baseURL: project.baseURL, code: enrollment.code});
// Open it from a Connect WDC button; retain URL/code as a manual fallback.
// Manual fallback: Connections → Connect server → Connect manually → enter URL/code.
const client = new RelayClient({
  ...project, connectionId: saved.connectionId, origin: enrollment.origin,
  account: { appId: enrollment.appId, appUser: String(authenticatedUser.id) },
  groups: [{ id: 'ai', name: 'AI', kind: 'ai' }],
  authorize: async () => requireCurrentPermission(authenticatedUser.id),
});
await client.connect();
const admission = await client.preflight({kind:'action',group:'ai',prompt:'Check availability'}, ['text']);
if (!admission.available) throw new Error('Choose an available program in WDC.');
const job = await client.run({ group:'ai', prompt:'Summarize the supplied task.', idempotencyKey: persistedOperationId });
await client.close();
```

The app link opens the installed WDC app on its actual configured loopback port. It displays the relay's project, exact website and account for local approval, then opens program settings after the backend completes pairing and declares its groups. The URL carries only the five-minute single-use enrollment code and relay origin, never the project credential or desktop administration key. Keep these short-lived links out of analytics and logs. If app launch is blocked or unavailable, show **Open WDC again**, an install/update link and a collapsed manual fallback: **Connections → Connect server → Connect manually**, relay URL and code with copy buttons. No local port entry is required.

Keep pending enrollment separate from the active connection. Reuse a live pending invitation on repeated clicks; cancellation revokes just that invitation. Promote the replacement only after local approval and cryptographic connection, then retire the old connection. Backend status returns `enrollmentStatus: pending | declined | expired` for an unclaimed code, scoped to its project/account. Offline devices stay paired. `relaySettingsURL({baseURL, connectionId})` opens an already-paired connection's program settings; it cannot create a pairing or grant execution. The local panel verifies both values against its existing binding.

After local approval, choose programs/models for the app and feature groups in WDC. A new pairing does not automatically select a provider or grant automatic execution. Local approvals, Restricted/App tools/Workspace/Commands boundaries and sharing controls remain authoritative.

For workflow options, use SDK 0.1.1 and an updated desktop advertising `routingVersion: 2`; pass `minimumRoutingVersion: 2` when constructing the client. `routingConfigured` reports whether an app default has been chosen, without sharing model names. SDK 0.1.0 direct integrations remain compatible and need no update. See [workflow routing](protocol.md#groups-sessions-diagnostics) for independent program/model/reasoning inheritance and migration aliases.

The backend client also provides capabilities, groups, jobs/status/lookup/cancel, verified streaming snapshots, continuing `session()` conversations, upload/removeInput, artifact download/hash validation, and registered app-tool callbacks. See [the SDK API](../sdk/index.ts) for their types. `authorize` runs before every request, heartbeat and tool claim/result. Tool execution must enforce the application's domain authorization and use the stable callback ID for any external mutation. WDC claims callbacks once; an uncertain claim is never executed again. Kings requests no folders or shell access.

## Ownership and failure recovery

Project authentication and opaque connection IDs scope every request. Supply `X-WDC-Account` as a second ownership check. One connection is bound to project, immutable account, device identity and relay signing key. Pairing codes expire after five minutes and are single-use. The relay rejects browser Origins and exposes no CORS access.

The relay carries existing protocol-v2 signed ECDH/AES-GCM messages to the same local dispatcher. Requests additionally bind the relay connection and deadline; signatures cover operation, path and request hash. Every output frame and terminal result is authenticated and chained. Local provider credentials are not part of the protocol. This is confidentiality from network intermediaries; the trusted project backend and self-hosted relay can read the requested content.

Connector polls last 25 seconds. Backend and connector leases have 45 seconds of grace; direct browser leases remain 12 seconds. Keep a backend client alive for owned background work, explicitly persist its owner/device/route/fallback snapshot and stable inference IDs, and renew only while authorized. Close/revoke cancels local work. Revocation or device loss must pause a resumable run rather than consume another inference. Native sessions do not survive desktop process restart.

Before submitting, use admission to choose WDC or an explicitly permitted cloud route. After submitting, recover with `lookup(originalKey)`/`get(jobId)`. Never replay an uncertain operation with a new key or through cloud inference. The relay reserves immutable receipts before mutations. Duplicate transport/facade IDs return 409; the backend SDK uses fresh transport IDs while the caller preserves its inference key. Configure official SDKs with automatic retries disabled. If no receipt/job can confirm the outcome, stop for review.

Only live transport buffers hold prompts and output, bounded to 40-second exchanges, 64 active exchanges globally, 16 per connection and 64 MiB per exchange. SQLite stores project identities/credential hashes, connection identities, five-minute tickets and hash-only replay receipts; no prompt, attachment or output bodies are persisted. Shared deployment limits are 256 projects (including revoked identities), 4096 connections/tickets, one million receipts and a 512 MiB SQLite main database. Transport receipts retire only after two minutes, when their mandatory request deadline has expired; stale envelopes are always rejected. Facade inference receipts are never expired into permission to replay. Reaching a limit fails closed and requires an operator to provision a fresh relay identity and re-pair. Native history/files retain WDC's existing local limits. Back up and erase relay state as sensitive project data.

## Provider-format compatibility

Use the explicit `wdc-default` alias. It selects the user's locally chosen `ai` route; API format never identifies the executed native model. Names, native model/version and usage are returned only when supplied and shared. Missing token usage is `null`/absent, not an invented zero.

| Format | Routes | Supported |
|---|---|---|
| OpenAI | `/v1/responses`, `/v1/chat/completions` | Text, instructions/history, inline PNG/JPEG, supported JSON schema, ordinary and streamed text |
| Anthropic | `/v1/messages` | Text, system/history, base64 PNG/JPEG, supported JSON schema, ordinary and streamed text |
| Gemini | `/v1beta/models/wdc-default:generateContent`, `:streamGenerateContent` | Text, system/history, inline PNG/JPEG, supported JSON schema, ordinary and SSE text |
| Backend npm client | `/relay/v1/...` | Full WDC operation/session/file/artifact/app-tool surface |

Headers: the project bearer key (Anthropic `x-api-key` / Google `x-goog-api-key` also accepted), `X-WDC-Connection`, `X-WDC-Account`, and a unique `Idempotency-Key` (8–128 characters). Google streaming uses `alt=sse`. Unsupported top-level options and unsupported tool/file/audio/video content fail before inference. Remote image URLs are never fetched. At most seven inline images (8 MiB each) plus 128 KiB task context are accepted, within the total HTTP limit. JSON uses WDC's explicit subset: required object properties, no additional properties, scalar/array/object types, enum and descriptions; no references, unions or executable validators. Anthropic accepts `output_config.format` with `type: json_schema`; Gemini accepts `responseSchema` or `responseJsonSchema` within the same WDC subset.

`store: false` is accepted; provider-side persistence is rejected. Provider sampling controls, reasoning knobs, hosted tools, provider file stores, previous-response IDs, API-side sessions, token counting, caching and provider batch APIs are unsupported. Explicit `max_tokens`/`max_output_tokens`/`max_completion_tokens` are prompt budget hints; they cannot override native-client hard limits. They are not billing or hard token-ceiling guarantees. Use RelayClient for continuing native sessions and artifacts. The APIs return routing alias `wdc-default`; they never claim an Azure deployment or a Gemini/Anthropic model solely because of their wire format.

Streaming contracts follow [OpenAI Responses](https://developers.openai.com/api/docs/guides/streaming-responses), [Anthropic Messages](https://platform.claude.com/docs/en/build-with-claude/streaming) and [Gemini generation](https://ai.google.dev/api/generate-content). Official SDK contract tests include streaming final-response/message helpers, errors and unsupported options. See `tests/relay-contract` and `scripts/relay-e2e.mjs`.

## Validation

Run `npm ci --prefix tests/relay-contract`, `go test -race ./internal/relay ./internal/bridge`, `npm run test:package`, and `node scripts/relay-e2e.mjs` after building `.cache/bin/webdevicecable` and `wdc-relay:local`. `RORIDO_WDC_SDK=/path/to/rorido/apps/main-fe/node_modules/@webdevicecable/sdk/index.js` exercises Rorido's installed published browser package. `--native` additionally runs synthetic Codex/Claude text, JSON, session, callback and Codex image-generation/edit checks through the Docker relay. All harness state and volumes are disposable; no existing app database is used.

After packaging, set `WDC_RELAY_SDK=dist/relay-client/cjs/relay.js` or `WDC_RELAY_SDK=dist/relay-client/relay.js` to exercise the shipped CommonJS or ESM entry with the same Docker harness. The harness always registers its projects through the management API and checks independent projects on one desktop, cross-project isolation, key rotation, restart and project revocation alongside the direct client. CI exercises both formats and verifies their runtime exports and TypeScript declarations.
