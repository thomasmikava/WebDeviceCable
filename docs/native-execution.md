# Native execution

WDC invokes the user's unmodified installed Codex client over documented app-server stdio, or Claude Code through `claude -p` with streaming JSON. It does not implement OAuth, read/copy authentication files, expose provider credentials, call inference endpoints directly, or select paid API billing. Both clients use their existing native subscription sign-ins.

Approved HTTPS and loopback website origins are accepted. WDC connects to the local installed CLI, rather than exposing a provider listener to websites. Provider local/open-source versus commercial/hosted conditions remain a separate deployment responsibility; WDC does not infer them from the website origin. See [provider review](provider-review.md).

## What runs

- Incremental text, continuing sessions, schema-checked JSON, supplied PNG/JPEG images and UTF-8 text/Markdown/JSON documents.
- Codex-only image generation/editing using the built-in native image tool. Actual returned PNG bytes become private WDC artifacts. WDC ignores native `savedPath` values and never reads arbitrary provider-returned paths.
- Actual native version, resolved model, reasoning when confirmed, and token usage when reported. Claude usage reflects the native CLI result; WDC does not fabricate a billing estimate. Optional program/version/model fields respect the existing sharing switches.

Settings checks the installed client automatically on runner startup. **Check** repeats discovery without inference. Missing login, API-key-only authentication, unsupported isolation controls and managed-policy conflicts produce distinct status messages. macOS discovery also checks standard Homebrew/native installation locations when GUI PATH omits them. The installed client remains responsible for sign-in and native limits.

## Restricted execution

Each independent job/session gets a private working directory and an ephemeral native thread. No `--continue`, most-recent history, shared thread ID or model-generated resume command is used. Continuing turns stay in the same owned process and thread. Native sessions remain alive while their tab lease is maintained and close on a failed/cancelled turn. The runner kills the process group/Windows Job Object on cancellation, lease loss, pause, revoke, timeout or shutdown. Interrupted requests are never resubmitted.

Before inference WDC checks native account metadata, requires the provider's native subscription authentication and privately pins an installation-scoped account fingerprint to the pairing, separately for Codex and Claude. It checks the account again before every turn. Switching programs does not change either pin; changing the account within one program requires a new pairing. Fingerprints/native thread IDs never appear in remote responses; no account token is requested.

For Codex, a named permissions profile permits minimal OS runtime reads and the private working directory only, with network disabled for sandboxed tools. Shell, browsing, computer use, application connectors, user hooks, plugins, extra agents and host skill discovery tools are disabled. A metadata-only probe discovers inherited integrations without receiving a model prompt. WDC then starts the execution process with every inherited MCP server/plugin/app and discovered skill explicitly disabled, and verifies their effective disabled state before creating its thread. Empty configuration tables alone do not clear inherited entries. Own instructions and zero project-document allowance prevent inherited AGENTS instructions. WDC validates effective feature/filesystem controls, profile provenance, network/approval policy and instruction sources before submitting a turn. Unexpected native tools/approvals/authentication requests are rejected and the session ends. App tools/Workspace add only explicitly registered WDC dynamic tools; ordinary Restricted requests add none.

The built-in image tool and registered dynamic tools additionally require Codex's local native code-mode host. Shell and network tools remain disabled. Other integrations remain disabled. Codex may keep its own generated-image cache under its native data directory. That copy is native-client storage: WDC neither reads it nor promises to erase it. Clear history deletes WDC artifact copies only. Native authentication/cache access is performed by the trusted official client, outside model tool permissions.

## Claude Code controls

Requires Claude Code 2.1.308+ and an existing personal Pro/Max sign-in. WDC invokes `auth status` without reading credentials and retains the OS user identity needed for native keychain access. API keys, alternate provider endpoints, executable runtime options and subscription-token environment variables are excluded.

Each process runs `--safe-mode --restricted`, with an empty built-in tool list, denied MCP tools, a strict empty MCP configuration, disabled slash commands/browser integration/hooks/connectors/memory, no user/project/local setting sources, and unanswered permissions denied. WDC supplies its own system prompt. It verifies the session ID, working directory, native version, empty tool/MCP/skill lists, native authentication source and permission mode in each turn's initialization event. Unexpected tools, hooks, control requests or cross-session events end the session.

Only the CLI's built-in components remain; WDC does not modify the client. Administrator policies can re-enable hooks despite safe mode. Organization accounts, nonempty managed policy/cache files, OS-managed policy and WSL therefore fail with a specific isolation reason; WDC neither overrides those policies nor asks users to weaken them. OS policy locations and the native-reported configuration directory are checked read-only before each turn. Native Linux/Windows policy checks are cross-compiled but still need native runtime validation.

Input is passed on standard input as structured user messages, never interpolated into a shell or CLI arguments. Image bytes are base64 blocks; text documents are inline blocks. The same process/context serves a continuing session with `--no-session-persistence`; no native transcript is resumed or copied. Per-turn schema instructions are followed by WDC's strict local JSON validation, so invalid output fails without retries or additional billing. Native account identity is checked and privately pinned before every turn. Image generation/editing remains unavailable through Claude.

## File and JSON limits

`upload()` transfers arbitrary file bytes in signed/encrypted 24 KiB chunks. Files belong to one pairing and tab lease; incomplete files, changed hashes and other tabs' IDs are rejected. Limits are eight inputs per tab, 8 MiB per file, 16 MiB per request and 64 MiB total reserved input memory. Expiry/revocation removes uploads. Direct native input further limits UTF-8 documents to 128 KiB and supports only PNG/JPEG and text/Markdown/JSON. Other formats can be transported to file-enabled configured commands; remote URLs and arbitrary input paths are never fetched.

Generated PNGs are checked for content, dimensions and size, capped at four files per turn, 8 MiB each and 64 MiB retained WDC storage. The SDK downloads authenticated chunks and checks the whole-file SHA-256 hash. Clear terminal history to free WDC output storage. Image requests fail if no native image was returned; prose claiming success never creates a successful image job.

`outputSchema` requires an object at its root and accepts an explicit bounded JSON Schema subset: single `type`, `properties`, `required`, `additionalProperties:false`, `items`, `enum`, `title`, `description`. All object properties must be required; types are object/array/string/number/integer/boolean/null. No references, schema downloads or executable validators. Maximum 8 KiB / 12 levels. Unsupported schemas fail before inference. WDC validates the completed JSON again and returns `job.json`; malformed, mismatching or truncated output fails. Text output defaults to 1 MiB per turn (`bridge.maxTextBytes`, 4 KiB–2 MiB). Exceeding the limit fails explicitly with partial output retained. Numeric validation preserves decimal precision (up to 256 literal characters and an exponent magnitude of 1,000,000).

## Validation and release limits

The opt-in `node scripts/native-e2e.mjs --images` uses real existing subscription usage. It exercises approval, encrypted streaming, native context continuation, text-document/schema input, image generation/editing, encrypted file transfer, metadata and cancellation. The `--claude` variant checks real Claude Code streaming, context continuation, supplied inputs, schema validation, metadata omission and cancellation. Standard checks use fixtures/connection tests and never send model prompts.

Validated natively on macOS with Codex `0.163.0-alpha.10.2` and Claude Code `2.1.308`; the app-server interface and named profiles remain version-sensitive. Native Windows/Linux provider sandbox/runtime and clean-machine installation checks are explicitly pending early-user testing in the community prerelease; WDC fixture/installer tests pass on native CI runners. Signing/notarization needs publisher credentials; an independent security review is recommended separately. Unsupported client/policy combinations fail closed instead of falling back to broader permissions.


## App tools and Workspace

Codex receives a bounded explicit `dynamicTools` list; only `item/tool/call` requests bound to the active thread/turn reach WDC's dispatcher. All other native command, file-change, integration, approval and authentication requests remain denied. The local code-mode host is enabled only when needed for images or registered tools, with the same verified native filesystem/network profile.

Claude's `--safe-mode` disables even explicitly configured MCP servers, so tool-enabled sessions use a separate documented Restricted profile: built-in tools off, strict WDC-only MCP, empty setting sources, hooks/connectors/memory/skill/plugin synchronization off, instruction-file exclusions, no Chrome or saved sessions, and only the exact WDC MCP names allowed. Startup verifies that exact tool/server list, no skills and no external plugins. An incompatible profile fails the affected request, preserving ordinary tool-free chat. The bundled Go executable also serves as the private stdio MCP adapter; it has only a short-lived conversation capability and never handles provider credentials.

Folder reads and edits happen through WDC's `os.Root` dispatcher, not native shell commands. Grants record folder identity, reject traversal/escaping links and refuse WDC/provider configuration roots. Edits show a bounded preview and check file hashes again before replacement. Revoking or changing grants ends affected contexts. Unknown native control requests never become blanket permission.

WDC's file transport itself accepts arbitrary bytes. Direct native input currently supports PNG/JPEG and bounded UTF-8 text/Markdown/JSON; other formats require a capable configured command. Native adapters report unsupported inputs rather than converting them or substituting API calls.
