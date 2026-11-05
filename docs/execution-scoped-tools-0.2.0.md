# Execution-scoped tools: 0.2.0 release handoff

Prepared on 5 November 2026. At the end of implementation, nothing had been published, deployed, installed, tagged or committed. The user subsequently authorized publishing both npm packages on 5 November 2026. Desktop/relay deployment and installation remain separate release actions.

## Implemented behavior

- Direct and relay SDKs share execution methods and immutable tool snapshots. Tools belong to a continuing session or standalone job, never a pairing/client constructor. Constructor tools and `registerTools()` were removed.
- Sessions open lazily, retain their definitions/handler references, and reject replacement through messages or resume options. Catalog changes require a new native context with application-supplied history.
- Desktop-computed canonical hashes bind execution metadata and inference receipts. Callback routing includes job/session/request/hash identity; handlers are installed before submission, claimed once and dispatched with four concurrent callbacks per client. Completion polling uses owner-bound receipts even after history deletion. Execution/session/client closure releases registered handlers and aborts signals; heartbeats report owner-scoped closed sessions.
- Inline definitions have explicit budgets: 32 tools; 8 KiB/12-level schemas; 1 MiB definitions; 1,088 KiB execution/preflight bodies; 40 KiB non-tool/ordinary bodies; 4 MiB encrypted envelopes. Desktop live definitions are capped at 8 MiB before receipt reservation. Relay batches remain eight packets/4 MiB combined bodies with bounded 8 MiB poll responses.
- Authenticated oversized logical requests return explicit 413 validation errors. New clients require `scopedToolsVersion:1` from desktop and authenticated relay capabilities. Signed/encrypted protocol v2, pairing identities, folder permissions and old receipts remain intact.
- Rorido connects without fetching tools. Each turn carries `tools`/`toolsVersion`; changed catalogs rebuild bounded saved history and replace only that chat's session. Callbacks verify execution context without a model-supplied chat argument. Rule/syllabus/quiz work uses tool-free standalone jobs.
- Kings retains its per-inference relay client and saved pairing identity. Support tools go to preflight and actual submission; callbacks recheck authorization. Ordinary inference remains tool-free. Existing receipt recovery and pre-admission-only fallback remain in place.

## Validation

All checks below passed against disposable state or fixture persistence, without accessing/resetting application databases:

| Check | Result |
| --- | --- |
| Go vet and full Go race suite | Passed on macOS arm64; additional relay capability/authentication/size race tests passed |
| SDK types, generated-output drift and tests | Passed; 79 tests including early callbacks, concurrent 30/3 catalogs, overlapping IDs/schemas/handlers, four-call dispatch, ambiguous claims, cleanup, size boundaries, update gates and the review regressions below |
| npm archive consumers | Both archives installed/imported/typechecked under ESM, CommonJS and bundler resolution; browser HTTP imports and account-scoped storage passed |
| HTTP/commands/installed-intent fixtures | Passed; streamed output, pairing, leases, tab isolation, cancellation, persistent identity and receipt recovery |
| Direct and relay scoped HTTP | Passed concurrent 30/3 admissions, distinct hashes, cancellation, changed-tool idempotency conflicts and maximum 32-tool catalogs with control characters, near-limit schemas and a maximum-length prompt |
| Docker relay | Passed non-root execution, direct/relay concurrency, files, project/account isolation, credential rotation, restart recovery and revocation |
| Real Codex and Claude | Both passed direct and relay 30/3 execution isolation, different schemas/handlers for overlapping IDs, actual callbacks, continuing memory and explicit history-backed replacement |
| Rorido | Both app typechecks, backend build, scoped lint, 54 backend tests and 77 frontend tests passed. Real encrypted HTTP accepted the actual 43,379-byte catalog at validation time |
| Kings | Shared backend typecheck, scoped lint and 30 WDC/support tests passed; authorization fault injection caused the intended test failure and was restored |

Native smoke verification tokens are generated inside callbacks and never appear in prompts or definitions. A model's assertion that it called a tool cannot satisfy the smoke test. No native-provider check was skipped. Native execution was tested on macOS; Windows/Linux runtime and installation checks are not claimed by cross-compilation.

Encoding regressions cover literal HTML data surviving schema normalization and state cloning without inflating the byte budget, and canonical Unicode escapes being checked before admission.

## Review follow-up

All three review findings were fixed with regressions that failed before their fixes:

- SDK callback registrations survive conflicting submissions, matching retries and delayed failures from a previous client connection. Matching attempts share their snapshot until all attempts are definitively rejected, or their accepted execution ends. Unknown outcomes retain handlers for receipt recovery.
- Session creation checks the client generation before installing a snapshot. Delayed responses cannot restore handlers after closure or overwrite a newer connection's scopes.
- Rorido cancels known unfinished standalone jobs when response verification fails or streaming ends early. Abort and failure cleanup share one cancellation request; completed jobs are not canceled.

The SDK adds seven regressions and Rorido adds three. SDK/package checks, direct/relay HTTP integrations, Docker integration and consumer checks were rerun against rebuilt local archives. Earlier Go race and real native-provider results remain valid for unchanged Go execution/provider code. Logs ending in `review-before-fix` deliberately show the expected regression failures, not outstanding failed checks.

## Local artifacts

- npm: `dist/webdevicecable-sdk-0.2.0.tgz` and `dist/webdevicecable-relay-0.2.0.tgz`.
- Desktop preview downloads: `dist/scoped-tools-0.2.0/`. These are **unsigned preview** artifacts, not publisher-signed/notarized releases. Existing installed WDC and its state were left untouched.
- Relay: local image `wdc-relay:scoped-tools-0.2.0`, plus exported image/binary archives under the same artifact directory. See its manifests for platform coverage.
- `npm-artifacts.json` records exact npm integrity hashes; application lockfiles contain registry versions/integrities, never local filesystem dependencies. The exact archives were extracted into ignored application `node_modules` for validation. A clean registry install requires later publication of these versions.
- `validation/` contains command logs. `CANDIDATE-SHA256SUMS` covers deliverables. These ignored local artifacts are not source commits.

Rebuild locally without publishing/installing:

```sh
npm run package:sdk
BRIDGE_GO="$PWD/.cache/toolchain/go/bin/go" python3 scripts/package.py --out-dir dist/scoped-tools-0.2.0
```

## Later release actions

The user's npm publication request is now authorized. Review/commit the intended source, verify tarball hashes against both lockfiles, then publish the exact prepared npm archives. Check registry state before any publication attempt; versions cannot be replaced. If an archive is rebuilt with different bytes, refresh both the affected lock integrity and consumer validation. Official signing/notarization, deployment and replacing the user's installed WDC remain separate actions. Make compatible desktop/relay and SDK releases available before deploying the application upgrades. No application database migration is needed.

Other developers are actively changing Rorido themes and related MCP definitions. Preserve their work. The Rorido changes owned by this task are its two SDK manifests, `pnpm-lock.yaml`, local-AI PRD, Rori local schema, `rori.service.ts`, `rori-local.spec.ts`, `rori-wdc-client.ts`/spec and `WebDeviceCableIntegration.spec.tsx`. Kings changes are its shared-backend manifest, `yarn.lock`, WDC service/spec and WDC feature documentation. Do not sweep unrelated working-tree changes into a release commit.
