# Contributing to WebDeviceCable

Development requires Go 1.25+, Node.js 22+ and npm. Python 3.12+ is needed only for packaging; building macOS launchers also needs Xcode command-line tools. Packaged applications require none of these development tools.

```sh
npm ci --ignore-scripts
npm run build:sdk
go build -o dist/WebDeviceCable ./cmd/webdevicecable
BRIDGE_GO=go scripts/check.sh
npm run test:e2e
node scripts/cli-e2e.mjs
npm run test:package
```

The integration scripts also accept the existing `dist/webdevicecable` development binary. Use `--data-dir "$PWD/.local/dev"` for local experiments. Never run tests against your normal installation or another project's data. Standard tests use fixtures and do not require provider accounts, publisher certificates or signing keys. Tests and generated packages belong in ignored `.cache`, `.local` and `dist` directories.

Read [the integration guide](docs/integrate.md), [security boundaries](docs/security.md) and [release guide](docs/releases.md) before changing execution or transport. Add regression coverage for a security boundary or a changed lifecycle. Preserve existing pairing identities, exact-origin checks, mandatory consent and the signed/encrypted protocol. Never automatically replay uncertain work or substitute paid API calls.

Native provider tests are opt-in and use the operator's subscription. Run them only deliberately with the normal OS profile; coding-agent environments may select different native accounts. Do not add account credentials to fixtures or logs.

Fork and pull-request builds are unsigned development builds and never install official updates. Fork maintainers must use their own release keys and replace the repository/feed URLs before distributing their own official builds. Never borrow WebDeviceCable's publisher identity.

Keep changes focused and describe the behavior, reproduction and validation. Do not include personal state, control links, invitations, real prompts or secrets in issues, screenshots, tests or pull requests. Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md). Contributions are under the existing MIT license; provider trademarks retain their owners' rights.
