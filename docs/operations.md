# Operating and releasing WDC

## Everyday use

Open the installed app, or run `webdevicecable start`. The local runner serves the UI independently of the WDC panel; requesting app tabs own their jobs through leases. `webdevicecable open` reopens the panel. `webdevicecable stop` cancels work and stops it. `serve --data-dir PATH --port 0` is useful for an isolated development instance. No system service, administrator privileges, database or remote relay is required.

The desktop app stays in the macOS menu bar or Windows/Linux system tray without opening a terminal. Click its WDC icon to open the control panel in your default browser; this also restarts the runner if it was stopped. Opening the app again reuses the existing icon and opens the panel. Right-click (or Control-click on macOS) for **Open WebDeviceCable** and **Quit WebDeviceCable**. Quit stops the runner and cancels outstanding work before removing the icon. Closing the browser leaves WDC and its icon running. Open the `.app`, Windows Start-menu shortcut or Linux applications-menu shortcut to create the icon. The bare `start` CLI command does not create it.

On Linux, native StatusNotifier/AppIndicator support provides the icon and its menu; desktop environments choose its placement and may show the menu on the first click. GNOME installations without tray support need that support enabled to show the icon. WDC retains the applications-menu shortcut and opens the web panel even if no tray host is available. On Windows the OS may place a newly installed icon in the tray overflow. No C compiler, GTK runtime, Node.js or Python is required by the tray launcher.

Settings checks installed native clients without inference. A **Connection test** exercises the local runner without using an AI subscription. Install/update/sign in to Codex or Claude Code using its official client. WDC does not install clients or manage their credentials. Claude requires version 2.1.308+, a personal Pro/Max sign-in and compatible policy. Unsupported client controls or modes have specific status messages.

Start WDC with the same OS profile and native-client environment you normally use. Coding agents may supply an isolated `HOME`, `CODEX_HOME` or `CLAUDE_CONFIG_DIR`, which can select a different native login even when WDC uses the usual `--data-dir`. If an approved account suddenly differs, stop that development runner and restart from the normal device profile before considering re-pairing. Never clear account pins or copy provider credential files to work around the check.

## Update, backup and recovery

Quit the desktop app and stop the runner before replacing an installation. Install the new binary/app for the same OS/architecture, then start it using the **same data directory**. The supplied Linux/Windows installers use `desktop-stop` to close the native tray app before replacing its executable, then stop the default runner. Keep a known-good copy for rollback. Verify `SHA256SUMS` against the downloaded archive; hashes alone do not authenticate a publisher. Current preview artifacts are unsigned.

The directory is normally the OS user config directory plus `webdevicecable`, or the existing legacy `ai-bridge` directory. `--data-dir` overrides it. It contains sensitive state, keys, pairing grants, command configuration, minimal replay receipts and output artifacts. Use a local per-user filesystem with private permissions; do not use a shared/network directory. Unix uses owner-only modes; Windows applies a protected current-user/SYSTEM DACL and inheritance. Same-account processes and administrators are outside this storage boundary.

For backup, stop WDC and copy the **whole directory** to encrypted private storage. Restore the directory as a unit to the same user's device only if no requests have executed since that backup; never run two live copies of one installation identity. If the backup predates executed requests, create a fresh data directory, copy only your command configuration, and re-pair apps instead of reusing an older identity/ledger. Preserve `identity.key`, `admin.key`, `state.json` and `receipts/` together. Deleting/rolling back receipts can allow consumed requests to be submitted again. A deliberately new data directory creates a new installation requiring re-pairing. Corrupt/oversized files fail closed and remain available for recovery; do not discard keys or receipt files to bypass an error. Restore a consistent backup or explicitly create a fresh installation.

Terminal prompts are removed. History retains up to 500 jobs and 20 MiB of output/JSON, automatically removing the oldest finished job and its WDC artifacts when admitting another. Clear history erases retained terminal output and WDC artifacts, keeping minimal hashed replay receipts. Native Codex has its own cache and privacy settings. Stopping/restarting never replays a job; outstanding work becomes interrupted. Unix configured programs that deliberately detach descendants can outlive process-group cancellation; only grant commands you trust. Commands retain OS-account filesystem/network rights.

The launcher verifies a challenge bound to the saved loopback origin and PID before opening its private URL or sending shutdown credentials. An unrelated process on an old port fails authentication. If startup fails, inspect the private `runner.log`; avoid sharing control links, invitations, configuration secrets or state files in bug reports. `version`, OS/architecture and the friendly Settings status usually suffice.

To uninstall, stop WDC and remove the application (macOS), or run bundled `uninstall.sh` / `Uninstall.cmd`. User state is deliberately retained so a later reinstall preserves grants and identity. Delete the data directory separately only when intentionally resetting the installation and its replay history.

## Validation and release checks

`scripts/check.sh` runs format/vet/race checks, SDK type/build/test checks and theme contrast checks. `npm run test:e2e` uses the real binary and encrypted SDK, including commands and continuing sessions, without inference. `node scripts/cli-e2e.mjs` checks launcher authentication and start/open/stop. `npm run test:package` checks installation/import of the local SDK tarball. `python3 scripts/package.py` builds six platform/architecture targets, SDK modules, docs, checksums and a dependency/build manifest. Go and npm vulnerability checks are separate from tests. CI runs the non-inference checks on Linux, macOS and Windows; adding the workflow does not establish that those remote runs have passed.

The opt-in `node scripts/native-e2e.mjs --images` makes actual Codex subscription calls: streaming, continuing context, structured JSON, supplied files, image generation/editing, metadata/privacy and cancellation. Use `node scripts/native-e2e.mjs --claude` for real Claude Code streaming, continuation, JSON/files and cancellation. Run these deliberately with your native account. Provider interfaces are version-sensitive; unsupported or conflicting isolation settings must fail closed.

`node scripts/native-tools-e2e.mjs codex` (or `claude`) separately tests a real app callback, a selected-folder read and a write approved through WDC. Add `--idle` to check continuing context after 65 idle seconds. These tests intentionally use the installed native subscription and create disposable state and folders inside this repository.

For public distribution:

1. Obtain successful native Linux and Windows execution/installer/browser results, including cancellation and Windows DACL checks; cross-compilation is not equivalent. Validate the macOS app/package on a clean supported machine.
2. Optionally sign the release using `scripts/sign-macos.sh` / `scripts/sign-windows.ps1` and publisher credentials. A GitHub download does not require Apple/Microsoft review. Notarization is an automated Apple scan; it is separate from App Store review. Unsigned packages remain identifiable as previews.
3. An independent security review is recommended. It is an external assessment, not an Apple/Microsoft distribution requirement or a prerequisite for finishing local implementation.
4. Keep native subscription eligibility within current provider conditions. Provider conditions for commercial/hosted Codex use remain separate from accepted HTTPS website origins. Claude is a user-controlled local invocation of the official CLI; third-party product distribution conditions still require the maintainer's review. Never extract/relay credentials or disguise API billing as subscription usage.

The current packages remain **unsigned previews**. App tools and Workspace are implemented; native compatibility is checked per request. No build script commits, uploads or publishes anything.

App tool authorization belongs to the connected app. Older per-tool WDC grants are discarded when state is next saved; pairing keys, folder grants and configured-command consent remain intact. Older SDK tool-permission requests complete immediately. New clients can check `appToolPermissions:"application"` before using this policy.
