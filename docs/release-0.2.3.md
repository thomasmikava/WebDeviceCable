# WebDeviceCable desktop 0.2.3

Windows downloads are now single EXE setup installers. Download
`WebDeviceCable-0.2.3-Windows-x64-Setup.exe` for Intel or AMD PCs, or
`WebDeviceCable-0.2.3-Windows-ARM64-Setup.exe` for Windows on Arm.
No ZIP extraction is needed.

The setup wizard installs for the current Windows account without administrator
access, creates the Start-menu shortcut and URL association, and offers to open
WDC when finished. It upgrades existing ZIP/script installations in place.
Installed Apps removal remains available. Upgrades and removal preserve private
settings, pairing identities, permissions and inference receipts.

Mac downloads use `macOS-Apple-Silicon` or `macOS-Intel`; Linux downloads use
`Linux-x64` or `Linux-ARM64`. The installation guide explains which to choose.
Previously published downloads keep their original names and checksums.

This is a desktop packaging release. The 0.2.2 SDK packages and relay runtime
remain compatible and are not republished. There are no API or state-format
changes and no application database migration is required.

The community test channel retains manual updates, ad-hoc macOS app signatures,
no notarization and unsigned Windows installers. The complete checksum list uses
the existing community release key. See [installation](../INSTALL.md) and
[release verification](releases.md).

Release checks build all six OS/architecture targets and verify embedded source
manifests, signed checksums and compatibility with previous release downloads.
Windows CI exercises fresh EXE installation, repeat installation, upgrades from
the script installer, architecture rejection and removal while retaining private
data. Native provider execution on Windows/Linux and clean-machine OS/browser
approval remain early-user checks; installer tests do not establish those results.
