# Application requirements

Open-source local bridge between independently paired apps, installed Codex/Claude Code and explicitly configured local programs.
Kings and Rorido are examples, not dependencies; keep changes in this repository.

## Delivery
- One self-contained executable with a bundled local web UI on Linux, macOS and Windows; no Node.js or Python required by users.
- A local runner stays available independently of its control panel; optional self-hosted relay with outbound polling. Official WebDeviceCable releases support signed manual/automatic updates, waiting for active work to end.
- Browser SDK and independent backend npm package and a documented signed/encrypted HTTP streaming protocol for other languages; no unsigned fallback.
- Each pairing binds an application identity/origin, device and app user; every user supplies their own native client login.
- Reuse supported native authentication without reading, copying, or exposing provider credentials; never silently use paid APIs.

## Capabilities
- Stream text and validated JSON; transport supplied files unchanged, with format support owned by the selected program.
- Image generation/editing is Codex-only; advertise it only after subscription-backed automation and artifact isolation are verified.
- Return output files/metadata, actual program/provider/model/version and usage only when known; local sharing switches omit optional execution metadata entirely.
- Support app-owned tool callbacks and selected folders with remembered local grants and write approvals; support OliveTin-style exec actions in config.yaml, with web editing disabled unless file-enabled before restart.
- Persist queued jobs; support reconnect, cancellation, timeouts and duplicate prevention without retrying interrupted work.
- Each requesting browser tab or owned backend operation has a bounded job lease; tab closure/disconnection cancels its jobs after a grace period.
- Offer per-job approval or explicit automatic execution within the pairing's granted scopes.
- App-declared AI/command groups support optional subgroups, inherited local selections and model preferences; leased sessions preserve context, with normalized errors and optional diagnostics.
- Respect personal-account privacy settings; explain which provider receives data.

## Release boundaries
- Check current official native-client docs and terms; technical feasibility does not establish permission.
- Fail closed for unsupported modes or incompatible native controls; report concrete reasons in UI and protocol. Document provider distribution conditions separately.
- Local connection tests must be explicitly labeled, make no AI requests and never impersonate provider output.
- Self-hosting transports requests, never provider credentials; no credential extraction, modified clients, subscription pooling, resale or limit evasion.
- Keep architecture small and these definition files at most 30 lines each.
