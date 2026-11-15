# WebDeviceCable desktop 0.2.6

Redesigns the control panel's connection cards and dialogs. Each card is one
panel with AI program, Access and Settings columns that show their current
values. Per-connection overrides move out of Access into their own Settings
dialog, with Custom tags and a reset button. Access uses switches and value
rows, and Add connection separates the app-started path from its two
alternatives. Programs that have not been checked yet show "Checking…" instead
of "Unavailable" while the startup check runs.

Settings gains defaults for new connections: maximum request time, output size
limit, connection expiry and request history. History can be kept until
cleared, kept for a chosen time (1 second to 365 days), or not kept, and each
connection can override these defaults. Existing history settings migrate to
the matching policy. Timed cleanup runs during maintenance and at startup and
keeps replay receipts. Linux and Windows installs gain an opt-in Start at login
switch.

Builds use Go 1.27.4, which fixes a net/http vulnerability.

Existing connections, SDK packages and relay deployments remain compatible. No
database migration is needed beyond the automatic history-setting migration;
building this version does not publish npm packages. Community downloads retain
manual updates, ad-hoc macOS app signing and unsigned Windows installers.
