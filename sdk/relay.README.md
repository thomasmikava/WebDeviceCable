# @webdevicecable/relay

Node backend client for a self-hosted WebDeviceCable (WDC) relay. Zero runtime dependencies. Requires Node.js 22+. CommonJS, ESM and TypeScript declarations are included; both module entries share the same runtime and error classes.

TypeScript backend projects should install `@types/node` as a development dependency. Browser DOM declarations are not required.

The relay forwards requests to the user's paired desktop over its outbound connection. Native execution and provider credentials stay on the desktop. The relay server ships separately as Docker. This package must stay on the backend.

## Install

```sh
npm install --save-exact @webdevicecable/relay@@VERSION@
```

```js
import { RelayClient, RelayAdminClient } from '@webdevicecable/relay';
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
// Show the returned pairing instructions to this authenticated account.
// After local approval, retain its connection ID on the backend.
```

See [RELAY.md](./RELAY.md) for the complete pairing flow, `RelayClient` construction, groups, jobs, streaming, sessions, files, tool callbacks, provider-compatible routes and recovery. Your backend owns authorization, operation IDs and output validation. Never automatically replay an uncertain inference through another route.

The package does not include the Docker server or desktop application. For direct browser integration, use [`@webdevicecable/sdk`](https://github.com/thomasmikava/WebDeviceCable/blob/HEAD/docs/integrate.md). MIT licensed.
