# Integrate WebDeviceCable

WDC runs on your user's computer. Your browser app talks directly to it with signed, encrypted requests and streaming responses. The direct transport needs no backend or relay. For a server-to-desktop integration, use the separate [Node relay package and Docker server](relay.md); provider credentials remain local. End users install one binary; they do not need Node.js or Python.

**Availability:** approved HTTPS and loopback origins can use installed Codex or Claude Code, configured commands, app tools and selected folders. Images are generated/edited through Codex only. File transport accepts arbitrary bytes; the selected program determines what it can read. Native authentication, account policy and isolation checks still apply. Check capabilities; never substitute paid APIs. [Provider conditions](provider-review.md) concern deployment and distribution, rather than an HTTPS-origin ban.

## 1. Install and include the SDK

Have the user install WDC ([OS instructions](../README.md#run)). It normally listens at `http://127.0.0.1:47321`; Settings shows native-client readiness. The user signs in using the official installed Codex or Claude Code client. The Connect button can launch installed WDC and the user approves there.

The browser package is `@webdevicecable/sdk`. After the corresponding npm release is published, install it into your frontend and let your build serve it with the rest of your app:

```sh
npm install --save-exact @webdevicecable/sdk@0.1.0
```

```js
import { BridgeClient, ConnectionStore } from '@webdevicecable/sdk';
```

Both ESM and CommonJS frontend tooling are supported, with TypeScript declarations. Native browsers use the ESM build. No runtime dependencies or install scripts are required. The package contains only client code; users still install WDC separately.

For a page without a build step, use a version-pinned URL after publication:

```html
<script type="module">
  import { BridgeClient, ConnectionStore }
    from 'https://unpkg.com/@webdevicecable/sdk@0.1.0/index.js';
</script>
```

The same files are available from `https://cdn.jsdelivr.net/npm/@webdevicecable/sdk@0.1.0/index.js`. These are native ES modules, not a global script. Pin the full version; both URLs require that version to have been published.

Each WDC archive also includes `sdk/`. Copy `index.js`, `secure.js` and `storage.js` from the same release into your app's static assets and import `./wdc/index.js`. TypeScript declarations are included. Before the first npm release, or for offline use, install the **local** archive instead:

```sh
npm install ./webdevicecable-sdk-0.1.0.tgz
```

Then import from `@webdevicecable/sdk`. From source, `npm ci --ignore-scripts && npm run package:sdk` builds the archive under `dist/`; it never publishes. See [npm distribution and the release procedure](npm.md) for package ownership, publishing and switching reviewed snapshots. The examples below use self-hosted files; use the package import above when bundling.

## 2. Pair, save and connect

Use a stable application ID and the current signed-in user's immutable ID. Never use one shared account ID for all users. Run on HTTPS or localhost, with WebCrypto, IndexedDB and streaming fetch available. Adapt these element IDs to your app:

```html
<button id="connect" disabled>Connect local device</button>
<button id="send" disabled>Send</button>
<button id="cancel" disabled>Cancel</button>
<textarea id="prompt" aria-label="Message"></textarea>
<pre id="output" role="status"></pre>
<p id="error" role="alert"></p>
<script type="module" src="./app.js"></script>
```

Put the following in `app.js`, replacing `signedInUser.id` with your authenticated account ID. Recreate this setup for each account change.

```js
import { BridgeClient, ConnectionStore } from './wdc/index.js';

const account = { appId: 'your.app', appUser: signedInUser.id };
const store = new ConnectionStore(account);
const $ = id => document.getElementById(id);
let connection, bridge, current, busy = false;
const showError = error => { $('error').textContent = error.message; };

// Restore on page load, before the click. Storage may be unavailable in private browsing.
try { connection = await store.load(); } catch (error) { showError(error); }
$('connect').disabled = false;
$('connect').onclick = async () => {
  $('connect').disabled = true;
  try {
    // Call directly in the click handler: a popup needs browser user activation.
    if (!connection) {
      ({ connection } = await BridgeClient.requestPairing({ ...account, name: 'Your app' }));
      await store.save(connection).catch(showError); // Memory still works for this visit.
    }
    bridge = new BridgeClient({ connection, account, groups: [
      { id: 'ai', name: 'AI', kind: 'ai', children: [
        { id: 'chat', name: 'Chat', kind: 'ai' }
      ] },
      { id: 'export', name: 'Export' } // Command-only; subgroups are optional.
    ] });
    await bridge.connect();
    $('send').disabled = false;
  } catch (error) { showError(error); $('connect').disabled = false; }
};

$('send').onclick = async () => {
  if (busy) return;
  busy = true; $('send').disabled = true; $('error').textContent = '';
  try {
    current = await bridge.submit({ kind: 'action', group: 'ai.chat',
      prompt: $('prompt').value, idempotencyKey: crypto.randomUUID() });
    $('cancel').disabled = false;
    for await (const job of bridge.stream(current.id)) {
      $('output').textContent = job.output || job.detail || job.status;
      if (job.error) showError(job.error);
    }
  } catch (error) { showError(error); }
  finally { busy = false; $('send').disabled = false; $('cancel').disabled = true; }
};
$('cancel').onclick = () => { if (current) void bridge.cancel(current.id).catch(showError); };

// Call from your logout/account-change handler before switching users.
async function disconnectAccount() {
  try { await bridge?.close(); } finally { await store.clear(); connection = undefined; }
}
```

The user approves the connection in WDC's **Connections** page, then chooses **Default program** on its card. Groups inherit their parent's choice; a group can override it in **Program settings**. Declaring a group never grants a command. **Request approval** defaults to **Ask each time**; **Run automatically** requires explicit consent. Tell users when a request is awaiting approval. The included [working browser example](../examples/app.js) also supports manual invitations, continuing chats, commands and forgetting a connection (`node examples/server.mjs` during development).

If application launching, popups or browser local-network permissions prevent connection, offer manual pairing: the user creates an invitation in WDC's authenticated panel, then your app calls `BridgeClient.pair({invitation, ...account})`. Treat invitations as secrets. Never automatically replace a pinned runner key or reset a failed pairing. A revoked/lost pairing needs a new user-approved invitation; a paused connection needs the user to resume it. Forgetting local storage does not revoke the old grant: the user can revoke it in WDC.

## 3. Choose the right helper

| Need | SDK call |
| --- | --- |
| Check available features and granted commands | `await bridge.capabilities()` and `await bridge.actions()` |
| Connection test, no AI request | `await bridge.run({kind:'connection-test'})` (still requires approval) |
| One request, final result or structured error | `await bridge.run({group:'ai.chat', prompt:'Hello'})` |
| Continuing chat/process with streaming | `const chat=bridge.session({group:'ai.chat'}); for await (const update of chat.stream('Hello')) { /* render snapshot */ }` |
| End chat; next chat starts fresh | `await chat.close()` |
| Run a granted command directly | `await bridge.run({action:'export', arguments:{format:'csv'}})` |
| Supply a file | `const input=await bridge.upload(file, file.name)`; pass `inputs:[input.id]` |
| Generate/edit image with Codex | Submit `kind:'image-generate'` / `'image-edit'`, `provider:'codex'`; editing needs a supplied image |
| Download output | `const blob=await bridge.artifact(job)`; display/download via an object URL, then revoke the URL |
| Schema-checked JSON | Pass `outputSchema`; read final `job.json` ([supported schema subset and limits](native-execution.md#file-and-json-limits)) |

Render each stream update as a **replacement snapshot**, not an appended token. Responses remain incremental. `run()` throws on failed terminal jobs; with `submit()/stream()`, inspect terminal status and `job.error`. Aborting a stream stops reading; **`cancel(job.id)` stops execution**. One `BridgeClient` per tab; release it when that workflow ends. Do not maintain leases from your backend or a service worker. Background suspension can end a lease after 12 seconds; closed/expired sessions never resume silently. Persist the pairing, not the lease or chat ID.

## Errors, browser settings and trust

- `routing_required`: ask the user to choose this group's default in WDC. `command_not_granted`: user grants the current command definition. Native-unavailable errors: check capability reasons and WDC Settings.
- `submission_unknown`: keep the exact request and `error.idempotencyKey`. Call `lookup(key)` or deliberately retry that **same** input/key. Do not generate a replacement key until the outcome is resolved. `410 history_deleted` means consumed and no longer retained; never replay it. Other retryable errors permit an explicit user retry, not automatic execution.
- `identity_mismatch` / `verification_failed`: stop. Check the signed-in account, installed runner and approved pairing. Do not accept unsigned data or silently trust a new key.
- Browser local-network permission may be required. Your CSP needs `connect-src http://127.0.0.1:47321` (or the user's exact endpoint). Test HTTPS, popup policy and local-network permission in supported browsers. Do not proxy these requests through your server. To use another port, pass the same `baseURL` during pairing and client construction; its identity remains pinned.
- Pairings bind app ID, exact origin, app user and device. Browser-held private keys are non-extractable; WDC never receives them. The runner key stays local. **Your app/browser sees plaintext** after verification/decryption. XSS in your own origin can use its pairing: keep your own CSP and account isolation sound. These signatures authenticate pinned keys, not a remotely attested installation or your app's backend.
- Optional execution names, versions, models and diagnostics can be omitted by WDC's sharing settings. Handle absent fields; never infer metadata or depend on it for authorization. Use the current packaged SDK with protocol v2; there is no unsigned fallback.

For non-browser implementations use the [complete protocol](protocol.md), and for advanced groups/sessions/errors see [SDK reference](sdk.md). Programs are declared in the user's [config.yaml](commands.md), never supplied as arbitrary commands by websites.


## Optional app tools and folders

Register tool definitions and callbacks when constructing the client. WDC checks input schemas before dispatch. App callbacks need no per-tool WDC grant. Your app must authorize each callback, provide any needed confirmations, and deduplicate mutations using the supplied call ID. WDC validates schemas and keeps callbacks inside the originating tab/account.

```js
const bridge = new BridgeClient({ connection, account, tools: [{
  id: 'tasks.create', title: 'Create task', description: 'Create a task in this account.',
  inputSchema: { type: 'object', properties: {title: {type: 'string'}},
    required: ['title'], additionalProperties: false },
  async execute({title}, {idempotencyKey, signal}) {
    return createTaskForCurrentAccount({title, idempotencyKey, signal});
  }
}] });
await bridge.connect();
await bridge.requestPermissions({workspace: true});
// Folder access is approved in WDC; existing folder grants do not ask again.
const chat = bridge.session({group: 'ai.chat', mode: 'workspace'});
for await (const job of chat.stream('Read the approved folder and create a task.')) {
  render(job.output);
}
```

Use `mode:'app-tools'` for app callbacks without folders. Default `restricted` exposes neither. Folder paths are chosen only in WDC; the app receives grant IDs and labels. Folder edits ask for WDC approval unless the user separately enables automatic edits. Your app owns confirmation for app actions. `readOnly:true` is an application declaration, not a substitute for backend authorization. Tool definitions are hashed; changing them ends affected native sessions. Folder grant edits/revocation end affected Workspace sessions. Start a new session explicitly after the user finishes reviewing access.

In WDC, the user opens the connection's **Folder access**, chooses **Add folder**, browses to the folder and adds it. They review read/edit access and choose **Save access**. Browsing or cancelling never grants access; newly added folders default to read-only.

A callback is claimed once before it runs. Reconnects never replay it. Use the call ID as your backend idempotency key: a tab can close after a remote mutation succeeds but before its result arrives. Cancellation cannot undo an already-performed external action. Return bounded JSON (32 KiB); use your own file references for larger results. Unknown outcomes are never silently retried.

File-enabled commands use `files:true` in the owner's config.yaml; websites cannot enable it. `upload()` and `artifact()` are unchanged. File bytes are staged unchanged, and unsupported native formats produce an explicit error. WDC does not convert documents, parse PDFs or add OCR. See [command file interface](commands.md#file-transfers).

## WebDeviceCable updates

Official installations update after active sessions finish. A verified `bridge_updating` error means the runner temporarily stopped accepting work. Keep the UI state and reconnect after it returns, using the same stored pairing and pinned identity. Do not resubmit uncertain jobs automatically: use the existing idempotency lookup to resolve their status. Explicitly ended sessions need a new session; never restore a native context silently or switch to paid credits without the app user's choice. Paired apps cannot install updates or change update preferences.
