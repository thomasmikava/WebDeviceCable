# WebDeviceCable 0.2.2

This coordinated release updates the desktop, relay runtime,
`@webdevicecable/sdk` and `@webdevicecable/relay`.

## Changes

- Connections have a visible **Permissions** button. Automatic execution, file
  transfer and folder grants share one draft and one atomic **Save changes**.
  Folder navigation preserves edits; closing a dirty draft asks before discarding.
- A monotonic permission revision rejects stale drafts before applying any
  setting. A draft opened before another window revokes access cannot restore it.
  Durable, payload-bound save receipts recover lost acknowledgements without
  reapplying permissions. Uncertain saves offer explicit check/retry controls and
  explain that closing does not undo a change already applied.
- **Send and receive files** is an independent connection permission. Apps may
  suggest it during pairing; the owner reviews it. Existing connections start
  with files off. Revocation cancels file-bearing work, clears pending uploads
  and closes continuing contexts that already received files.
- `uploadContext()` supplies bounded large UTF-8 text without a file grant.
  `toolResult()` attaches pairing- and lease-bound files to application callback
  results, allowing the chat AI to inspect original images and documents.
- Native adapters support images and PDF, DOC, DOCX, XLS and XLSX document
  representations alongside text formats. Generic file transport has no
  extension allowlist; provider capabilities determine how inputs can be used.
- Codex receives tool-result image pixels in the exact active turn. Its RPC
  reader queues interleaved callbacks and preserves accepted application results
  if subsequent file conversion or delivery fails. These failures report that
  the operation must not be repeated automatically.
- Claude stream validation handles documented auxiliary events while retaining
  primary-session identity checks and diagnostics for rejected session events.

## Upgrade

Make the 0.2.2 desktop and relay runtime available before deploying consumers of
the new SDK APIs. Pin npm dependencies to `0.2.2`. Existing pairing identities,
account bindings, other permissions and inference receipts are preserved. No
application database migration is required. Owners of existing connections must
enable **Send and receive files** before an app can use file operations.

Desktop downloads use the community test channel: manual updates, ad-hoc macOS
app signatures and unsigned Windows executables. They are not notarized. The
complete checksum list is signed with the existing committed community release
key; see [verification](releases.md). Publishing artifacts does not deploy relay
or application servers or replace a running desktop installation.

## Validation

The local candidate passed full Go race tests, 91 SDK tests, SDK types and
generated-output checks. Disposable browser checks cover shared drafts, scoped
revocation, stale conflicts, lost and malformed save acknowledgements, uncertain
outcomes, durable retry and reload, keyboard focus, and 320/390 px layouts.
Real Codex and Claude checks on macOS exercised direct and relay image inputs,
file tool results, continuing visual context, file revocation and receipt lookup
with disposable state and synthetic application callbacks.

The GitHub release records final source, package-consumer, transport and
cross-platform CI results. Native provider execution on Windows/Linux and
clean-machine browser/OS approval remain early-user validation criteria. Provider
inference availability depends on the user's installed client and account.
