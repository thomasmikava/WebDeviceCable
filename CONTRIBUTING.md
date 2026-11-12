# Contributing to WebDeviceCable

Development requires Go 1.25+, Node.js 22+ and npm. Python 3.12+ is needed only for packaging; building macOS launchers also needs Xcode command-line tools. Packaged applications require none of these development tools.

```sh
npm ci --ignore-scripts
npm run build:sdk
./.tools/go/bin/go build -o dist/WebDeviceCable ./cmd/webdevicecable
BRIDGE_GO="$PWD/.tools/go/bin/go" scripts/check.sh
WDC_E2E_DATA_DIR="$PWD/.local/ux-finish" WDC_E2E_PORT=47461 npm run test:e2e
WDC_E2E_DATA_DIR="$PWD/.local/ux-finish" WDC_E2E_PORT=47461 node scripts/cli-e2e.mjs
npm run test:package
```

The integration scripts also accept the existing `dist/webdevicecable` development binary. Use `--data-dir "$PWD/.local/ux-finish" --port 47461` for local experiments. Never use port 47321, the default data directory, an OS URL handler or the installed app in development checks. The sample app requires an explicit isolated `BRIDGE_URL` and supports manual invitations and reconnection only; it cannot launch the native app. Never run tests against your normal installation or another project's data. The HTTP suites accept `WDC_E2E_DATA_DIR="$PWD/.local/ux-finish" WDC_E2E_PORT=47461`; the directory must be empty, directly under `.local/`, and is removed after each suite. Runtime URLs must belong to the child process and cannot use the installed port. Standard tests use fixtures and do not require provider accounts, publisher certificates or signing keys. Tests and generated packages belong in ignored `.cache`, `.local` and `dist` directories.

Read [the integration guide](docs/integrate.md), [security boundaries](docs/security.md) and [release guide](docs/releases.md) before changing execution or transport. Add regression coverage for a security boundary or a changed lifecycle. Preserve existing pairing identities, exact-origin checks, mandatory consent and the signed/encrypted protocol. Never automatically replay uncertain work or substitute paid API calls.

Native provider tests are opt-in and use the operator's subscription. Run them only deliberately with the normal OS profile; coding-agent environments may select different native accounts. Do not add account credentials to fixtures or logs.

Fork and pull-request builds are unsigned development builds and never install official updates. Fork maintainers must use their own release keys and replace the repository/feed URLs before distributing their own official builds. Never borrow WebDeviceCable's publisher identity.

Keep changes focused and describe the behavior, reproduction and validation. Do not include personal state, control links, invitations, real prompts or secrets in issues, screenshots, tests or pull requests. Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md). Contributions are under the existing MIT license; provider trademarks retain their owners' rights.

## UI palette

`npm run theme:generate` generates a new accessible dark palette; `npm run theme:check` validates its contrast. The generated palette is committed and stays fixed across launches. Theme generation runs only during development.

## Isolated browser development

Build the SDK and binary as above. Create an empty private fixture directory and disable native providers before starting the foreground runner:

```sh
mkdir -p .local/ux-finish
printf 'bridge:\n  disableNative: true\nactions: []\n' > .local/ux-finish/config.yaml
./dist/WebDeviceCable serve --data-dir "$PWD/.local/ux-finish" --port 47461
# In another terminal; open only the private panel URL printed by that foreground runner.
BRIDGE_URL=http://127.0.0.1:47461 BRIDGE_EXAMPLE_PORT=47462 node examples/server.mjs
```

Create the sample app's manual invitation in that isolated panel (app ID `writer.example`, origin `http://127.0.0.1:47462`, account `alice`). Use **Test connection**, which calls no AI provider. Stop both foreground servers with Ctrl-C when finished. Do not reuse this populated fixture directory for automated suites; those require an empty directory.

Control-panel lifecycle and development-isolation tests live under `scripts/` and run as part of `scripts/check.sh` (`node --test scripts/panel.test.mjs scripts/dev-isolation.test.mjs`). Only runtime HTML, JavaScript, CSS, SVG and PNG files are embedded; test files stay outside the asset directory.
