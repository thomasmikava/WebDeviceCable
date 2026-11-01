# Building and releasing WebDeviceCable

Distribution is through GitHub downloads. The first community test release supports manual installation with ad-hoc macOS signatures and unsigned Windows binaries. No paid developer account, personal signing name or email is required. The separate publisher-signed stable channel retains trusted certificates, macOS notarization and automatic updates. Source builds and previews remain supported. Apple/Microsoft store review is separate from code signing, and an independent security review is recommended rather than required to implement the product.

The browser and backend libraries have a separate [npm release procedure](npm.md), including local artifact checks, first publication and the protected publishing workflow. Their version comes from the same root manifest. Building desktop archives or finalizing update metadata never publishes npm packages.

## Preview builds

Use Python 3.12+, Go 1.25+, Node.js and npm. macOS packaging needs Xcode command-line tools.

```sh
npm ci --ignore-scripts
BRIDGE_GO=go python3 scripts/package.py --target darwin/arm64
```

Select any of the six documented targets. Windows and Linux can be cross-compiled; the macOS launcher requires a macOS build host. Packages use the WebDeviceCable name. Source/configuration directories and protocol identifiers retain their existing names for compatibility. All output is local under `dist`; no script publishes, uploads a release, commits or installs an application. Preview builds have automatic updates disabled and cannot install official updates.

## Community test release (manual updates)

Keep a persistent Ed25519 private key outside the checkout in a private directory. `keygen` creates it with owner-only permissions and refuses to overwrite it. Commit only its public PEM to `keys/community-release.pub`; this key authenticates download checksums, not an OS publisher identity. Back up the private key privately before relying on it for future releases.

```sh
node scripts/release.mjs keygen --private-key /private/location/community-release-private.pem
node scripts/release.mjs public-key --private-key /private/location/community-release-private.pem --pem > keys/community-release.pub
export WDC_RELEASE_PUBLIC_KEY="$(node scripts/release.mjs public-key --private-key /private/location/community-release-private.pem)"
BRIDGE_GO=go python3 scripts/package.py --community --out-dir .cache/community-release
node scripts/release.mjs prepare-community --dir .cache/community-release \
  --private-key /private/location/community-release-private.pem
node scripts/release.mjs verify-community --dir .cache/community-release \
  --public-key keys/community-release.pub
```

Build all six targets on macOS, where Xcode tools provide both native launcher architectures and `.pkg` creation. Commit the exact source before building/finalizing; archive manifests bind that commit, version, target, manual-update policy and public release key. Community preparation requires all six archives and both macOS installers, rejects mixed builds, and signs the complete checksum list. It never generates `updates.json` or Sparkle feeds. Community binaries remain unable to install official updates, including when saved preferences previously enabled them. macOS signs each executable and the app ad hoc; Windows binaries have no Authenticode signature. No machine-wide certificate trust changes are required or recommended.

Upload only the six runtime archives, two macOS installers, `INSTALL.md`, `build-manifest.json`, `RELEASE_PUBLIC_KEY.pem`, `SHA256SUMS` and `SHA256SUMS.sig` to a **draft prerelease** tagged `vVERSION`. Do not upload staging folders, local caches or private keys. The npm packages are already distributed independently; this release does not republish them. Check the final download list and publish the prerelease when ready to invite testers. Keep Windows/Linux native-provider and clean-machine browser checks explicitly pending for [early-user testing](testing.md). The separate signed stable channel still requires its native acceptance checks below.

## Signing keys

Publisher certificates, notary credentials, the WebDeviceCable release key and the Sparkle key are maintainer secrets. Store them outside the source repository, with restricted permissions and encrypted backups. Never pass private key material as a command-line argument, commit it, include it in an artifact or make it available to pull-request builds.

Generate the WebDeviceCable update key once using `node scripts/release.mjs keygen --private-key /private/location/release.pem`. The tool prints only the public key. Compile that value as `WDC_RELEASE_PUBLIC_KEY`. Use Sparkle's `generate_keys --account WebDeviceCable` for its independent key; export its private key to secure storage and use the printed public key for `WDC_SPARKLE_PUBLIC_KEY`. The Sparkle distribution is pinned to 2.10.1 and checked against GitHub's SHA-256 digest before extraction. Its full license is in the third-party notices.

Public update keys are expected to be public. Official builds pin them and do not accept replacement keys or feeds from websites. Keep old signing keys during a planned overlap release. Clients that miss a key transition may need to install a new verified package manually; losing the trusted private key never justifies disabling verification. Fork maintainers must change their public keys and the repository/feed constants in the update package, packaging script and release tool.

## Build signed downloads

Set `WDC_RELEASE_PUBLIC_KEY` on each native build host. Build Windows on Windows with `WDC_WINDOWS_CERTIFICATE` set to the thumbprint of a trusted Authenticode certificate. Build macOS on macOS with `WDC_SIGN_IDENTITY` (Developer ID Application), `WDC_INSTALLER_IDENTITY` (Developer ID Installer), `WDC_NOTARY_PROFILE` (an existing notarytool keychain profile), and `WDC_SPARKLE_PUBLIC_KEY`.

Run `python3 scripts/package.py --release --target OS/ARCH` for each target. Missing signing configuration fails the official build. Build both macOS architectures on macOS and both Windows architectures on Windows. Linux needs the update public key but has no additional OS signing certificate requirement.

The script builds, signs nested executables/frameworks and outer bundles, creates archives/installers, submits macOS software for notarization, staples tickets and verifies final artifacts before hashing them. The `.pkg` payload is copied from the final signed/stapled app. Windows executable and installer-script signatures are checked before ZIP creation. Signing changes bytes: never reuse checksums from a previous preview or modify signed files afterward.

Collect the six final runtime archives, both macOS installers and the SDK tarball in one local release directory. Finalize on macOS:

```sh
node scripts/release.mjs prepare --dir dist \
  --private-key /private/location/release.pem \
  --sparkle-dir .cache/sparkle-2.10.1 \
  --sparkle-key-file /private/location/sparkle.key
```

This verifies the archive manifests/version/public key, produces signed architecture-specific Sparkle feeds, signs `updates.json`, and writes final checksums. Keep build/validation evidence from each native signing host: finalization does not rerun Windows certificate checks on macOS. Review the exact files before manually creating a GitHub release tagged `vVERSION`. Upload the archives/installers, SDK tarball, `updates.json`, both appcast XML files and `SHA256SUMS` together before publishing. Never repoint an existing version to different bytes.

## Acceptance before publishing

Run the existing non-inference checks plus update tests. On native/clean OS environments, verify install, URL launch, one tray icon, idle update, busy-session deferral, explicit cancellation, restart, recovery and uninstall. Test Windows Installed Apps and macOS authorization cancellation. Test supported browsers' HTTPS/local-network behavior. Cross-compilation does not establish these results. Publisher credentials and actual notarization/signature results are external release requirements; CI simulations must never be described as real signatures.

Review current provider distribution conditions separately. Open-sourcing WebDeviceCable does not grant every integrating website unrestricted subscription-routing rights. No release should read provider credentials, disguise API billing or promise unsupported capabilities.
