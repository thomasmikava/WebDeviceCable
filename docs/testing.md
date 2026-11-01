# Community release testing

Start with [installation instructions](../INSTALL.md). Windows/Linux native Codex/Claude behavior and browser-download installation are deliberately being tested by early users. Automated cross-platform checks cover WDC itself, its encrypted transport, Windows install/removal and Linux tray lifecycle; they do not establish native-provider compatibility on every machine.

Use a throwaway Rorido workspace or synthetic prompts/files. Record WDC version, OS version/architecture, browser/version and the exact native client/version shown in Settings. No access to your credentials, data directory or normal documents is needed for a useful report.

1. Install from the downloaded archive. Open from the Start/applications menu or Applications, confirm one tray/menu-bar icon and reopen the control panel from it. Check the `webdevicecable:` launch from the website; invitation pairing is a separate fallback.
2. Run WDC's connection test before inference. Approve the correct website/account, choose program/model groups and confirm that a new connection cannot execute automatically until you allow it.
3. In Rorido, try chat with one harmless app tool, a rule clarification, a short syllabus and a dummy quiz rubric/grade. Check cancellation. Leave credit fallback off to verify that a disconnected desktop gives an error instead of consuming credits.
4. Where the installed Codex client supports it, generate a simple image and edit that image. Report unsupported capabilities exactly as shown; do not change native isolation settings to bypass a refusal.
5. Reload the requesting website, close its tab, sleep/wake the computer, stop/reopen WDC and pause/revoke the connection. Outstanding work should finish, cancel or become interrupted as appropriate; it must not silently repeat inference or app mutations.
6. Community builds show manual updates in Settings, with automatic controls disabled. Quit/reopen or reinstall the same build and confirm that pairings and permissions remain. Test a later-version upgrade when one is available; do not restore old state snapshots to simulate a downgrade.
7. Test uninstall and reinstallation only if you are comfortable doing so. Application files/shortcuts should be removed while private user state remains.

For a Kings relay test, an authorized admin must provision its relay/project and enable access first. Pair the desktop through Kings settings, then test direct Rorido and relay Kings concurrently. Teacher/student and unauthorized admin requests must retain Kings' existing cloud route. Close the Kings browser during an owned background run; direct Rorido work still belongs to its requesting tab. No production records need be modified for a transport report.

Report expected result, actual result, repeatable steps, the visible error and whether the issue happens with one or both native clients. Confirm when the failure is an OS installation block, browser permission denial, unsupported native client, or WDC request error. Redact personal metadata. Security reports belong in the repository's [private vulnerability reporting](https://github.com/thomasmikava/WebDeviceCable/security/advisories/new), not public issues.
