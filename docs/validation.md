# Validation record — 2026-10-26

Local environment: macOS arm64, Go 1.27.3, installed Codex 0.163.0-alpha.10.2 and Claude Code 2.1.308. The release/update changes remain uncommitted. Standard checks use isolated state inside this repository and make no inference requests. The opt-in native checks intentionally used the existing personal native sign-ins and subscription limits.

| Check | Result |
| --- | --- |
| Go format, vet and race-enabled tests | Passed; execution, permissions, transport, isolation, persistence and CLI |
| SDK TypeScript/build and generated-output consistency | Passed |
| SDK adversarial transport/lifecycle/persistence tests | 57 passed; signatures, encryption, Unicode fragments, tampering/replay, reconnection, callbacks, permission review and non-extractable keys |
| Real HTTP runner + SDK | Passed; pairing, mandatory consent, streaming, reconnection, tab leases/cancellation, revocation and restart/key pinning |
| Configured-command HTTP integration | Passed; grants, hashes, literal arguments, hierarchy, continuing processes, metadata privacy and cancellation |
| Installed-app launch rendezvous over real HTTP | Passed; intent, exact HTTPS origin, signed requests, encrypted invitation, consent and requesting-key binding; no OS handler registration performed |
| Opaque files and folder regressions | Passed; byte-identical command input/output, hashes, staging cleanup, symlink/traversal denial, replaced roots, stale edits and final lease/cancellation checks |
| App-tool regressions | Passed; app-managed dispatch without per-tool WDC grants, changed-definition invalidation, single claim, identical duplicate result, cross-account/tab isolation, folder revocation and private MCP capability/origin checks |
| Real detached CLI start/open/stop | Passed, including existing-runner reuse and runtime authentication |
| Local SDK tarball consumer | Installs/imports offline, zero runtime dependencies |
| Real native Codex app tools and Workspace | Passed: app callback, selected-folder read, write preview/approval and continuing context after 65 idle seconds |
| Real native Claude app tools and Workspace | Passed: app callback, selected-folder read and WDC-approved edit using the private bundled MCP adapter |
| Real native Claude Restricted regression | Passed: existing Max sign-in, consent, encrypted incremental output, continuing context, supplied PNG/text, JSON schema, hidden metadata, cancellation and an unsupplied-file canary |
| Earlier native Codex regression | Text/JSON/supplied-file sessions and actual image generation/editing passed earlier in this implementation; not repeated for the final permission-only fix |
| New permissions UI | Desktop browser check passed: read-only folder defaults, saved grants, access-request completion, reopening/persistence, focus and accessible controls; screenshot in `.local/ui-permissions.png` |
| Existing control UI/browser storage | Earlier desktop/390 px/320 px and real IndexedDB checks passed; the new permissions dialog was checked on desktop only |
| Theme | 35 contrast checks passed; existing random dark palette preserved |
| Distribution targets | Six Linux/macOS/Windows amd64/arm64 cross-builds; macOS native URL launcher and unsigned packages, SDK, docs, checksums and dependency/build manifest |
| Short application definitions | Requirements 29 lines; security 30 lines |

Additional Go coverage checks exact scheme/host/port matching, malformed ports, forged/replayed launch requests, encrypted fragment bounds, explicit output-limit failure, nonblocking FIFO handling and native profile enforcement. Windows DACL and junction boundaries compile here and are included in the OS test matrix; they have **not run natively on this Mac**.

Earlier Go vulnerability and npm development-dependency audits passed; no dependencies were added for these features. A locally added CI workflow is not a completed remote CI run.

Remaining external checks: native Linux/Windows execution, installers and URL associations; clean-machine macOS app/package installation and URL delivery; and target-browser HTTPS/local-network policies. Cross-compilation and HTTP rendezvous tests do not establish those results. Signing/notarization scripts are prepared but require publisher credentials and were not run. GitHub downloads require no Apple/Microsoft review; an independent security review remains recommended. Provider distribution eligibility is documented separately, without treating HTTPS origins as a native integration ban. See [release checks](operations.md#validation-and-release-checks).


## Release and update implementation — 26 October 2026

- Go format, vet and race checks, 57 SDK tests and 35 theme checks passed. Update regressions cover release signatures, wrong keys, malicious URLs/archives, downgrade protection, cancelled downloads, bounded cache retention, corrupt update settings, automatic-update preferences, active sessions/staged inputs, owner-only control and native authorization deferral.
- A real generic-helper fixture replaced a 0.1.0 executable with a 0.2.0 runner, authenticated its startup and preserved identity, control key, state and configuration bytes. Atomic replacement/recovery and short-lived, single-use panel handoff tests passed. This exercises the generic helper on macOS; it does not claim a signed Sparkle installation or native Windows runtime test.
- Existing HTTP/command/launch/CLI integrations and offline SDK consumer checks passed without model requests. The new SDK test verifies signed maintenance errors, no automatic job resubmission, reconnect under the original pin and refusal of a replacement signer.
- Six preview targets build, including both native macOS launcher architectures. The Sparkle-enabled Swift variant also compiles against checksum-verified Sparkle 2.10.1. These are unsigned previews; no publisher certificate or notarization credential was used.
- Browser inspection verified the concise dark Updates card and development-build restrictions in an isolated, native-disabled instance. Screenshots are private development artifacts under `.local/update-ui-qa`; no user connection or folder grants were changed.
- Release-key generation/public-key derivation and the missing-signing-configuration guard passed with a disposable test key, subsequently deleted. Final archive hashes were checked locally. No release was uploaded or published.
- Windows Installed Apps installation/removal checks were added to the Windows CI job but have not run on this Mac. Native Windows/Linux tray/install/update acceptance and clean-machine macOS signed update/authorization tests remain external release checks. Real signing, notarization and published update feeds require maintainer credentials and an explicit release.
