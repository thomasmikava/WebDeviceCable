# WebDeviceCable

[Source repository](https://github.com/thomasmikava/WebDeviceCable)

A small, open-source local bridge for independently paired apps and the user's installed, already signed-in Codex or Claude Code. One executable serves the same bundled web UI on Linux, macOS and Windows. No Node.js or Python is required to run a packaged build.

Codex and Claude Code support text streaming, continuing sessions, schema-checked JSON and supplied images/text documents through their installed clients. Image generation/editing is Codex-only. Configured commands, pairing, encrypted streaming, permissions, approvals, durable jobs and tab leases also work. Approved HTTPS and loopback websites can use native clients, registered app tools and selected folders. WDC transports arbitrary files to file-enabled commands without interpreting them. There is no paid API fallback. See [native execution and limits](docs/native-execution.md).

## Run

- **macOS:** install the `.pkg`, or drag `WebDeviceCable.app` from the ZIP into Applications. Open WebDeviceCable. Its menu bar icon stays available after you close the browser; click it to reopen the control panel. Right-click for **Quit WebDeviceCable**, which stops the runner.
- **Windows:** extract the ZIP and double-click `Install.cmd`. Open WebDeviceCable from the Start menu. Its system tray icon reopens the control panel; right-click for Open and Quit. Installation is per user and needs no Node.js or Python.
- **Linux:** extract the archive, run `./install.sh`, then open WebDeviceCable from the applications menu for its system tray icon. Or run `~/.local/bin/WebDeviceCable desktop`. The desktop must support StatusNotifier/AppIndicator icons; some GNOME setups need that support enabled. The applications-menu shortcut remains available without a tray host.

The first community test release uses ad-hoc macOS signing, unsigned Windows binaries and manual updates. No paid developer account is needed to build it. See [downloads](https://github.com/thomasmikava/WebDeviceCable/releases), [installation and OS warnings](INSTALL.md) and [the short testing checklist](docs/testing.md). Windows/Linux native-provider checks are part of early-user testing; unsupported clients fail closed. Trusted publisher-signed releases remain a separate distribution option.

`WebDeviceCable start` launches a detached local runner and opens its control panel. `WebDeviceCable open` reopens it; `WebDeviceCable stop` stops it. `serve` runs in the foreground. `--data-dir PATH`, `--port NUMBER`, `--no-open` and `open --print` support development and CLI use. Treat the printed private control link as a password.

The desktop app keeps a WDC icon in the macOS menu bar or Windows/Linux system tray after the browser closes. Click it to open the panel in your default browser; if your desktop shows a menu first, choose **Open WebDeviceCable**. **Quit WebDeviceCable** stops the runner and cancels outstanding work. Reopening the app reuses the same icon. The bare `start` CLI command runs without a desktop icon; Linux/Windows `desktop-stop` closes the tray app for updates.

In Settings, **Run test** creates a local test request. Approve it in Requests. This checks the connection without using a subscription or calling an AI model.

## Connect an app

Start with the [short integration guide](docs/integrate.md): installation, pairing, saved connections, streaming, cancellation and common errors. The browser package is `@webdevicecable/sdk`; backends use the independent `@webdevicecable/relay` package. Both support ESM, CommonJS and TypeScript. Browser ESM files can also be self-hosted or imported by a version-pinned CDN URL from the published npm release. See [npm distribution and release preparation](docs/npm.md). SDK files are bundled in desktop archives, and `npm run package:sdk` builds both local npm tarballs without publishing.

1. Click **Connect to WebDeviceCable** in the website. The SDK guides the user to open the installed app and puts the request in WDC's Connections page.
2. Approve the connection in WDC, then choose which program the app may use. Launching never grants access by itself.
3. If browser restrictions prevent launching, choose **Connect app** in WDC and copy the single-use, five-minute invitation into the website instead.

The app uses the bundled TypeScript SDK or documented HTTP protocol. It supplies its signed-in user's account ID; WDC does not independently authenticate third-party account claims.

WDC controls access to local folders and configured programs. Connected apps authorize their own tool callbacks and provide their own confirmations; there is no per-tool WDC approval screen.

Use **one SDK instance per requesting app tab**. The SDK's lease heartbeat keeps that tab's jobs alive. Closing it, a network failure, or browser suspension expires its lease within 12 seconds and cancels only its outstanding jobs. Closing the WebDeviceCable control panel has no effect on other app tabs. Apps must not move tab heartbeats into a backend/service worker that outlives the tab.

```ts
import { BridgeClient } from '@webdevicecable/sdk';

const origin = window.location.origin;
// Call directly from the website's Connect button handler.
const { connection } = await BridgeClient.requestPairing({
  name: 'Writer', appId: 'writer.example', appUser: signedInUser.id, origin,
});
const bridge = new BridgeClient({ connection, origin });
await bridge.connect();
const job = await bridge.submit({
  kind: 'connection-test', idempotencyKey: crypto.randomUUID(),
});
for await (const update of bridge.stream(job.id)) {
  renderRequest(update); // approval happens in the local control panel
}
// Explicitly release this tab's jobs when its workflow ends:
await bridge.close();
```

The SDK creates a separate, non-extractable signing key for each pairing. `connection` includes that browser `CryptoKey` and the runner's pinned public identity. Keep it in account-scoped memory, or store it using IndexedDB structured cloning; JSON/localStorage cannot preserve the private key. Clear it on account changes. Never put invitations or connection data in URLs, logs, analytics or public bundles. A backend's private key must never be shipped to browser JavaScript.

Requests and streamed chunks are signed and encrypted. The SDK verifies and decrypts each chunk before exposing it, so the requesting website can read and display the response. Signed nonces, request hashes and chunk chains detect substitution, replay and reordering. An interrupted stream reconnects to the current job without submitting it again. There is no unsigned fallback. Legacy pairings, lost runner keys or changed runner addresses require a new invitation; the SDK never silently trusts a replacement.

Chromium may ask for local-network access; HTTPS sites must be tested against the browser's local-network policy. The included browser example uses a loopback origin. This application protocol has automated security tests, but has not received an independent cryptographic audit.

## Groups and continuing sessions

Apps can declare AI groups or command-only groups, with optional subgroups. Users choose defaults and inherited overrides in Connections → Preferences. The SDK offers `run()`, `session().send/stream/run/close()` and app-initiated `requestPairing()` with requests shown prominently on Connections. Custom session commands keep the same owned process/context across messages. Codex sessions preserve the same native thread across messages. Settings can omit program, version and model metadata and control diagnostic sharing. See [SDK examples, hierarchy, errors and privacy](docs/sdk.md), or run the included browser example.

## Native-provider boundary

Custom programs are configured through `config.yaml` in the data directory; the Commands page shows its path. The [example configuration](examples/config.yaml) uses OliveTin-style `exec` actions. Restart after editing. The web editor is locked by default; enabling `bridge.allowWebCommandEditing: true` in that file and restarting allows editing separately stored web-managed actions. Websites cannot edit commands. Each website needs an explicit grant to the current command definition and OS-account permissions; commands are not sandboxed. See [command configuration, compatibility and lifecycle limits](docs/commands.md).

The integration invokes unmodified installed Codex and Claude Code with their existing native logins. It does not implement Sign in with ChatGPT/Claude or read/copy native auth files. Startup and **Check** inspect version, native account state and isolated-session compatibility without inference. Claude uses its official `auth status` diagnostic and `-p` streaming interface. Ambient API keys/provider URL overrides are excluded.

- **Codex:** implemented through documented app-server stdio for local applications. Both approved HTTPS and loopback website origins work. Provider conditions distinguish local/open-source integration from commercial/hosted services; an origin alone does not establish eligibility.
- **Images:** Codex's built-in image tool returns real PNG bytes. WDC validates and stores private artifacts; the SDK transfers signed/encrypted chunks. Native Codex may retain a separate cache copy. No API image substitute is used.
- **Claude Code:** implemented through `claude -p` with streaming JSON and an in-memory owned session. Restricted chat has no model tools; App tools/Workspace use only WDC’s private permission adapter. Requires version 2.1.308+ and an existing personal Pro/Max login. Managed-policy conflicts and WSL are currently unsupported because their isolation is not validated. Provider terms still apply; this is local CLI invocation, not a new Claude sign-in or a hosted credential relay.

See [provider review](docs/provider-review.md), [protocol](docs/protocol.md), [security notes](docs/security.md), and the two short [app definitions](docs/app-definition/requirements.md).

## Theme

The bundled UI uses a dark palette sampled by `node:crypto.randomInt` within the HSL ranges in `scripts/generate-theme.mjs`. The accent hue spans 0–359°. Candidates are rejected only for contrast failures; the selected palette stays fixed across launches. Run `npm run theme:generate` to sample a new palette, or `npm run theme:check` to verify the current one. No theme-generation code runs on the user’s computer.

## Updates

Community test installations use manual updates and never check for or install upstream updates automatically; Settings links to downloads. Quit WDC and install the new package over the existing app, preserving private state. Trusted publisher-signed installations check for and install stable updates automatically after requests and sessions finish. Settings → Updates provides manual updating and the automatic-update switch. Updates preserve connections and permissions. Source builds and previews do not install upstream updates. See [updates and recovery](docs/updates.md), [privacy](PRIVACY.md), and [maintainer release steps](docs/releases.md).

## Develop and validate

Development needs Go 1.25+ and Node.js (only for compiling/testing the small TypeScript SDK). The desktop uses file-backed state and has no frontend framework or database server. An optional [self-hosted Docker relay and backend npm client](docs/relay.md) add server-to-desktop transport with SQLite; the direct browser transport remains available.

```sh
npm ci --ignore-scripts --cache .cache/npm
npm run build:sdk
go build -o dist/WebDeviceCable ./cmd/webdevicecable
BRIDGE_GO=go scripts/check.sh
npm run test:e2e                          # real runner + SDK, no model calls
python3 scripts/package.py                 # six targets; macOS launcher needs Xcode command-line tools
node examples/server.mjs                   # development example, 127.0.0.1:47322
```

The generated SDK JavaScript/declarations are checked in so `go build` works without Node.js. To use a different Go executable set `BRIDGE_GO=/absolute/path/to/go`. The check/package scripts keep Go caches and test temporary files under this repository. Tests cover origin/Host/CSRF defenses, cross-app access, consent, leases, revocation, timeouts, restart interruption, duplicate prevention and SSE. Standard checks make no model requests. The opt-in `node scripts/native-e2e.mjs --images` exercises real Codex subscription execution; `node scripts/native-e2e.mjs --claude` exercises real Claude Code streaming, continuation, JSON/files and cancellation; run it deliberately. Set `bridge.disableNative: true` in config.yaml to disable native discovery/execution entirely.

Runtime state normally lives in the OS user config directory under `WebDeviceCable`. If that directory does not exist and a legacy `ai-bridge` data directory does, the launcher reuses it in place, preserving pairing keys, permissions and history. No files are moved. `--data-dir` always overrides this choice. Development/testing should use `--data-dir "$PWD/.local/dev"`. History retains up to 500 jobs and 20 MiB of response content, evicting the oldest finished job when needed. Minimal replay receipts are stored separately and survive history deletion and upgrades. A second runner cannot write the same directory. Pending jobs become interrupted after restart and are never replayed automatically.

See [operating and releasing WDC](docs/operations.md) for updates, backup/recovery, validation and the remaining release gates.

Contributions are welcome; see [CONTRIBUTING.md](CONTRIBUTING.md).

MIT licensed. Provider services and native clients retain their own terms and licenses. Bundled provider icons retain their owners’ rights; see [third-party notices](THIRD_PARTY_NOTICES.md).

Backend integrations: see the [relay deployment and Node package guide](docs/relay.md). One deployment supports multiple websites with dynamic operator registration and separate project credentials. Kings uses its own relay; Rorido remains a direct browser integration.
