# WebDeviceCable desktop 0.2.5

Fixes Codex being reported as not installed on Windows when its desktop app is
installed but its native CLI is absent from WDC's PATH. Automatic detection now
checks the desktop app's versioned runtime directory and uses the most recently
installed runtime containing `codex.exe`, skipping incomplete downloads and
other runtime components. A CLI on PATH or an explicitly configured executable
still takes precedence. Windows fallback paths use Windows executable lookup
rather than requiring Unix execute permission bits.

The existing native sign-in, account, and isolation checks still determine
whether the detected client is ready. No credentials are copied, and detection
does not start a model request. To return an existing Codex entry to automatic
detection after using an explicit-path workaround, reset that entry in Settings
and run **Check connection** after installing this release.

This is a desktop bug-fix release. Existing connections, SDK packages and relay
deployments remain compatible. No state-format or database migration is needed;
building this version does not publish npm packages. Community downloads retain
manual updates, ad-hoc macOS app signing and unsigned Windows installers.
