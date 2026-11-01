# WebDeviceCable

Connect your apps to the Codex or Claude Code client installed on your computer, using your own native sign-in.

WDC runs on **macOS, Windows and Linux**. It supports streaming text, structured JSON, continuing conversations, files and app tools. Codex also supports image generation and editing. Provider subscription limits still apply.

[Download WDC](https://github.com/thomasmikava/WebDeviceCable/releases) · [Installation instructions](INSTALL.md) · [Testing checklist](docs/testing.md)

## Run

1. Install and sign in to the official **Codex** or **Claude Code** client.
2. Install WDC and open it from Applications, the Start menu or your Linux applications menu. No Node.js or Python is needed.
3. In a supported app, click **Connect to WebDeviceCable**.
4. Approve the connection in WDC and choose the program/model that app may use. If launching fails, use invitation pairing instead.

WDC stays in your menu bar or system tray. Closing its control panel keeps it running; **Quit WebDeviceCable** stops it. Settings → **Run test** checks the connection without calling an AI model.

The community test release uses ad-hoc macOS signing, unsigned Windows binaries and **manual updates**. See the installation guide for OS approval steps. Native AI execution on Windows/Linux is still being tested by early users.

## Add WDC to your app

| Integration | npm package | Guide |
| --- | --- | --- |
| Browser → desktop directly | [`@webdevicecable/sdk`](https://www.npmjs.com/package/@webdevicecable/sdk) | [Browser integration](docs/integrate.md) |
| Backend → self-hosted relay → desktop | [`@webdevicecable/relay`](https://www.npmjs.com/package/@webdevicecable/relay) | [Backend integration and Docker setup](docs/relay.md) |

Both packages support ESM, CommonJS and TypeScript. One relay can serve multiple independently registered projects. Native provider credentials stay on the user's computer.

## Documentation

- [Capabilities and limits](docs/native-execution.md)
- [Permissions and security](docs/security.md) · [Privacy](PRIVACY.md)
- [Running, updating and troubleshooting](docs/operations.md)
- [Development and contributing](CONTRIBUTING.md) · [Building releases](docs/releases.md)

[MIT licensed](LICENSE). Native clients and provider services retain their own [terms and notices](THIRD_PARTY_NOTICES.md).
