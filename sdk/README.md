# @webdevicecable/sdk

Browser SDK for direct, signed and encrypted connections to the user's installed WebDeviceCable (WDC) application. Zero runtime dependencies. TypeScript declarations, native browser ES modules and CommonJS builds are included.

The user installs WDC and signs in to their installed native client. Your website needs no relay server for this transport. The npm package contains client code; it does not install WDC or include provider credentials.

## Install

```sh
npm install --save-exact @webdevicecable/sdk@@VERSION@
```

```js
import { BridgeClient, ConnectionStore } from '@webdevicecable/sdk';
```

CommonJS frontend tooling can use `require('@webdevicecable/sdk')`. Browser execution requires HTTPS or localhost, WebCrypto, IndexedDB and streaming fetch. Choose one module format per application; the native ESM and CommonJS builds have separate class identities.

Uses WDC's signed/encrypted protocol v2. The user chooses native programs and models inside WDC; the SDK does not include a provider model or a paid API fallback.

## Connect from a button

```js
const account = { appId: 'writer.example', appUser: signedInUser.id };
const store = new ConnectionStore(account);

// Call from the Connect button handler. The user approves in WDC.
const { connection } = await BridgeClient.requestPairing({
  ...account, name: 'Writer',
});
await store.save(connection);

const bridge = new BridgeClient({
  connection, account,
  groups: [{ id: 'chat', name: 'Chat', kind: 'ai' }],
});
await bridge.connect();
const job = await bridge.run({ group: 'chat', prompt: 'Hello' });
console.log(job.output);
```

Use the authenticated user's immutable ID. Keep one client per tab. Close the client and clear the account-scoped store on logout/account changes. Preserve the app's own authorization and output validation. See [INTEGRATE.md](./INTEGRATE.md) for lifecycle, streaming, consent and uncertain-outcome recovery.

## URL imports and self-hosting

The same npm release can be loaded without a bundler:

```html
<script type="module">
  import { BridgeClient, ConnectionStore }
    from 'https://unpkg.com/@webdevicecable/sdk@@VERSION@/index.js';
</script>
```

The equivalent jsDelivr URL is `https://cdn.jsdelivr.net/npm/@webdevicecable/sdk@@VERSION@/index.js`. Pin the complete version. Both URLs require that npm version to exist; building a local archive does not publish it. These are ES modules, not a global `window.WDC` script.

For self-hosting, copy `index.js`, `secure.js` and `storage.js` together from the package into one static directory and import its `index.js`. Keep the three files from the same release. Use the local `.tgz` artifact before the first npm publication.

For server-to-desktop connections, use the separate [`@webdevicecable/relay`](https://github.com/thomasmikava/WebDeviceCable/blob/HEAD/docs/relay.md) Node package and Docker relay. MIT licensed.
