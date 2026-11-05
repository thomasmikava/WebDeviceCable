# @webdevicecable/relay

Node backend client for a self-hosted WebDeviceCable (WDC) relay. Zero runtime dependencies. Requires Node.js 22+. CommonJS, ESM and TypeScript declarations are included; both module entries share the same runtime and error classes.

TypeScript backend projects should install `@types/node` as a development dependency. Browser DOM declarations are not required.

The relay forwards requests to the user's paired desktop over its outbound connection. Native execution and provider credentials stay on the desktop. The relay server ships separately as Docker. This package must stay on the backend.

## Install

```sh
npm install --save-exact @webdevicecable/relay@@VERSION@
```

```js
import { RelayClient, RelayAdminClient, relayConnectURL, relaySettingsURL } from '@webdevicecable/relay';
```

```js
const { RelayClient, RelayAdminClient } = require('@webdevicecable/relay');
```

Use the local `.tgz` artifact before the first npm publication. Neither installation requires a WDC source checkout or the browser package.

## Register each website

```js
const admin = new RelayAdminClient({
  baseURL: process.env.WDC_RELAY_URL,
  adminKey: process.env.WDC_ADMIN_KEY,
});
const { project, apiKey } = await admin.createProject({
  appId: 'writer.example', name: 'Writer', origin: 'https://writer.example',
});
// Save apiKey in that website's backend secret store; it is returned only once.
```

Keep the operator credential in the provisioning service. Give each website only its own project credential. Registration works for one website or many and requires no relay restart. A registered project cannot grant desktop permissions.

## Pair an authenticated account

```js
const enrollment = await RelayClient.enroll({
  baseURL: process.env.WDC_RELAY_URL,
  apiKey: process.env.WDC_PROJECT_KEY,
  appUser: String(authenticatedUser.id),
});
const launchUrl = relayConnectURL({baseURL: process.env.WDC_RELAY_URL, code: enrollment.code});
// Return launchUrl only to this authenticated account. It opens local approval.
// Keep code + URL under a manual fallback if the installed app cannot open.
// After local approval, retain its connection ID on the backend.
```

`relaySettingsURL({baseURL, connectionId})` reopens the paired connection's local program settings. Keep pending pairing separate from the active device until approval. `status()` distinguishes pending, declined and expired enrollment; a lost device remains paired until explicitly disconnected.

Workflow groups support independent program, model and reasoning choices in WDC. Use `minimumRoutingVersion: 2` to require the updated desktop; model choices and native credentials stay local. SDK 0.2.0 also requires desktop and relay `scopedToolsVersion:1`; upgrade both transports together.

Pass app tools to a continuing `session({tools})` or standalone `submit`, `run`, and `preflight`, never client construction. Sessions retain immutable definitions and handlers. New tools require an explicit new session with saved history; simultaneous executions can use different definitions for the same IDs.

See [RELAY.md](./RELAY.md) for the complete pairing flow, `RelayClient` construction, groups, jobs, streaming, sessions, files, tool callbacks, provider-compatible routes and recovery. Your backend owns authorization, operation IDs and output validation. Never automatically replay an uncertain inference through another route.

The package does not include the Docker server or desktop application. For direct browser integration, use [`@webdevicecable/sdk`](https://github.com/thomasmikava/WebDeviceCable/blob/HEAD/docs/integrate.md). MIT licensed.

Named installations and profiles are chosen locally in WDC, per connection and workflow. Existing `codex`/`claude` IDs remain the default entries; generated program IDs identify additional installations. Check each selected entry's capabilities before submitting work. Custom harnesses require explicit local grants and do not inherit native isolation guarantees. See [AI programs](https://github.com/thomasmikava/WebDeviceCable/blob/HEAD/docs/ai-programs.md).
