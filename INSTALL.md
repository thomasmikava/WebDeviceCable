# Install the WebDeviceCable community test release

Download only from [WebDeviceCable releases](https://github.com/thomasmikava/WebDeviceCable/releases). This is an early release for people who know the maintainer. macOS apps have ad-hoc signatures; Windows executables and macOS installers have no trusted publisher certificate. There is no notarization. No developer name or email is embedded as a signing identity.

## Choose a download

| Computer | Archive |
| --- | --- |
| Mac with Apple silicon | `darwin-arm64.zip` |
| Mac with an Intel processor | `darwin-amd64.zip` |
| Windows with Intel/AMD processor | `windows-amd64.zip` |
| Windows on Arm | `windows-arm64.zip` |
| Linux with Intel/AMD processor | `linux-amd64.tar.gz` |
| Linux on Arm64 | `linux-arm64.tar.gz` |

Each filename begins with `WebDeviceCable-VERSION-`. macOS also has `.pkg` installers. Published SHA-256 checksums cover the exact downloads; on macOS use `shasum -a 256 FILE`, on Linux `sha256sum FILE`, or on Windows `Get-FileHash FILE -Algorithm SHA256`, and compare with the release's `SHA256SUMS`.

The manifest has an independent Ed25519 signature. The public key is committed at `keys/community-release.pub`; release maintainers can verify it with `node scripts/release.mjs verify-community --dir DOWNLOAD_DIRECTORY --public-key TRUSTED_PUBLIC_KEY`. Use a key obtained from a trusted checkout or separately verified source, rather than trusting a replacement key supplied with an unknown download. This signature authenticates the release bytes; it does not remove OS installation warnings.

## Install and open

**macOS:** unzip and drag `WebDeviceCable.app` into Applications, then open it. If macOS reports an unidentified developer, use the app-specific **System Settings → Privacy & Security → Open Anyway** option after checking the source. The `.pkg` is an alternative that installs the same app. Do not turn off Gatekeeper globally. See [Apple's instructions](https://support.apple.com/102445).

**Windows:** extract the entire ZIP, then double-click `Install.cmd`. It installs for the current user and creates a Start-menu shortcut and URL association. If Windows shows an unrecognized-app warning, check the download source before using its app-specific continuation option. Some managed devices or Smart App Control configurations block unsigned apps entirely; report the message instead of disabling device-wide protection. See [Microsoft's explanation](https://learn.microsoft.com/en-us/windows/apps/package-and-deploy/smartscreen-reputation).

**Linux:** extract the archive, open a terminal in its folder and run `./install.sh`. Start WebDeviceCable from the applications menu, or run `~/.local/bin/WebDeviceCable desktop`. A desktop without StatusNotifier/AppIndicator support may not display a tray icon; the applications-menu shortcut and local control panel still work. No root install is needed.

No Node.js or Python is needed to run the packaged app. Install and sign in to the official Codex or Claude Code client separately using your own account. Open WDC's Settings and check its client status. An unsupported client or isolation configuration stays unavailable; WDC does not fall back to a paid API.

## First connection

1. In WDC Settings, use **Run test** and approve the request. This connection test makes no model call.
2. In Rorido, open **Settings → Integrations → WebDeviceCable** and connect. Approve the matching website/account in WDC, then select a locally installed program for its AI groups. If app launching fails, use the invitation pairing option.
3. A browser may ask for local/loopback network permission. Grant it only to the website you intend to connect. If denied, re-enable that site's permission and retry.
4. For a backend relay such as Kings, use the deployed relay URL and short-lived pairing code supplied by that project's authenticated settings page. Review the project/account in WDC before approving it. Rorido's direct connection needs no relay deployment.

Native inference consumes your installed client's subscription allowance. The first Windows/Linux native-provider tests are part of this release's feedback process, not a claim that every provider/OS combination has already passed.

## Update or uninstall

This community release uses **manual updates** and makes no automatic update checks. Quit WDC, download the next release and install over the existing application. Preserve its private data directory so connections, permissions and replay protection stay intact. Replacing a binary must never restore old application data or replay an uncertain request.

On macOS, remove the application to uninstall. Windows supports Installed Apps or the bundled `Uninstall.cmd`; Linux provides `uninstall.sh`. Uninstall deliberately retains private settings and history. Do not delete the data directory merely to retry failed work.

Please follow [the short testing checklist](https://github.com/thomasmikava/WebDeviceCable/blob/main/docs/testing.md). Report ordinary bugs with synthetic examples through GitHub Issues, and security issues through private vulnerability reporting. Never attach your state directory, login credentials, connection invitations, private control links or real prompts.
