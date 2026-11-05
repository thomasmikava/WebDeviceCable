# WebDeviceCable 0.2.1

This coordinated release includes the desktop, relay runtime and both npm
libraries, `@webdevicecable/sdk` and `@webdevicecable/relay`.

## Changes

- Apps can suggest a logo URL and automatic request approval during connection.
  WDC shows the request immediately, lets the owner change the approval choice,
  and keeps a connecting card visible until the connection is ready.
- Connected apps have an **Edit app** dialog for their local display name and
  logo, including image upload, URL preview and removal.
- Execution catalogs have no fixed tool-count cap. Existing byte budgets remain:
  1 MiB of definitions, 8 KiB / 12 levels per input schema, 1,088 KiB for execution
  creation, 40 KiB for ordinary requests and 8 MiB of aggregate live definitions.
- Tool inputs support optional/nullable fields, open objects, schema-valued
  additional properties, `anyOf`, `const` and numeric/string/array bounds. Output
  schemas retain their stricter contract. Unsupported numeric representations
  fail before execution, and decimal comparisons preserve precision.
- A dispatcher can report the resolved operation's ID, label and version through
  authenticated callback context. Activity and history show these details without
  treating them as authorization or routing inputs.
- Large Claude catalogs no longer expand the command line. Exact execution
  snapshots remain enforced at startup and on every callback.

The optional SDK features preserve existing pairings. Execution-scoped tools
continue to use the 0.2.0 API; constructor-level tools and `registerTools()` remain
removed. Applications own domain discovery, current-user authorization and
operation idempotency. New capabilities fail clearly against unsupported peers.

## Upgrade

Update the desktop and relay runtime before deploying applications that use the
new SDK features. Pin both npm libraries to `0.2.1` where applicable. Existing
pairings, account bindings, folder/command grants, identities and inference
receipts are preserved. No application database migration is required.

Desktop downloads use the existing community test channel: manual updates,
ad-hoc macOS app signatures and unsigned Windows executables. macOS builds are
not notarized. The checksum list is signed by the existing committed community
release key. See [installation](../INSTALL.md) and [release verification](releases.md).

## Validation

Local validation includes the full Go race suite and vet, 88 SDK tests, SDK
TypeScript and generated-output checks, installed ESM/CommonJS/package-consumer
checks, transport/launch/relay integrations and the connection editor UI.
Native Codex and Claude were exercised on macOS with disposable WDC state and
synthetic application operations. The final Claude launch fix was rechecked
through direct and relay connections with continuing context; the 4,000-tool
launch/isolation regression and numeric-schema cases also passed on Linux.

The published GitHub release records its exact source and cross-platform CI.
Native provider execution on Windows/Linux and clean-machine browser/OS approval
remain early-user testing criteria, as in the prior community release. Hosted
Rorido provider adapters were checked with fixtures rather than live paid APIs.

Publication does not deploy a relay, upgrade application servers, or replace an
existing desktop installation. Keep the private WDC data directory when updating
the application files.
