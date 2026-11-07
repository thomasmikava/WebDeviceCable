# WebDeviceCable desktop 0.2.4

Fixes Windows browser connection links being rejected after Windows adds a root
slash to the URL. Direct connections, relay connections and relay settings links
now accept that normalized form while retaining the existing validation of paths,
fragments and query fields. Includes the fix merged in PR #5.

Downloads include Windows x64 and ARM64 setup installers, macOS Intel and Apple
silicon installers and ZIP archives, and Linux x64 and ARM64 archives.

This is a desktop bug-fix release. Existing SDK packages and relay deployments
remain compatible; no API, state-format or application database migration is
required. Publishing this desktop release does not republish the npm packages.

Version 0.2.4 is published as GitHub's latest release. Downloads retain the
community distribution policy: manual updates, ad-hoc macOS app signatures,
no notarization and unsigned Windows installers. The complete checksum list is
signed with the existing community release key. See [installation](../INSTALL.md)
and [release verification](releases.md).

Native provider execution on Windows/Linux and clean-machine OS/browser approval
remain early-user checks. Cross-compilation and installer tests do not establish
those runtime results.
