# Provider review — 25 October 2026

Installed-client reuse is separate from a new OAuth integration. WDC never reads/copies provider credentials, patches clients, calls undocumented inference endpoints, evades usage limits, or silently selects API billing. Technical invocation is not proof of provider permission.

| Path | Technical evidence | Permission / implementation |
| --- | --- | --- |
| Codex native app-server stdio | Real installed-client streaming, continuing threads, JSON, image/text inputs, native images and cancellation passed. | Official auth documentation permits local/open-source clients and excludes commercial/hosted services. Implemented for loopback application origins; hosted origins fail closed. Integrators must also comply with the noncommercial/local allowance. |
| Codex native image generation/editing | Installed client's exported schema includes `imageGeneration` result bytes; real generation, encrypted file transfer and reference-image editing passed. | Uses the same eligible local native-client path and included plan limits. Image guide recommends the API for programmatic image generation; it does not establish blanket third-party/hosted subscription rights. No API substitute or hosted image route is implemented. |
| Claude Code unmodified binary | Real `claude -p` restricted/safe-mode streaming, continuing context, text-document/JSON, native metadata and cancellation passed on macOS. | Implemented as user-controlled local invocation with an existing personal Pro/Max login; no credential extraction or new sign-in. Anthropic documents CLI automation and subscription-backed third-party usage. Its separate product-distribution/authentication conditions still apply; this is not blanket permission for hosted credential routing or resale. |
| Sign in with ChatGPT | A separate integration route. | Not implemented; the requested design reuses an already signed-in native client. |
| Hosted relay / app-paid API | Other technical routes exist. | Out of scope; no hidden fallback. |

## Official sources reviewed

- [Codex app-server](https://learn.chatgpt.com/docs/app-server#auth-endpoints): local/open-source authentication allowance, commercial/hosted exclusion, stdio initialization, thread/turn events and native account metadata.
- [Codex permission profiles](https://learn.chatgpt.com/docs/permissions): named filesystem profiles with minimal runtime reads, path-specific access and disabled tool networking. Legacy `readOnly` does not provide the necessary restricted-root contract; WDC uses named profiles instead.
- [Codex configuration](https://learn.chatgpt.com/docs/config-file/config-reference): feature/configuration controls, native hooks, instruction files and inherited integrations. Empty maps merge and are insufficient to disable inherited entries.
- [Codex images](https://learn.chatgpt.com/docs/image-generation): built-in image generation/editing, included native plan limits, and separate API guidance. WDC does not claim universal image availability or third-party commercial eligibility.
- [Claude legal/compliance](https://code.claude.com/docs/en/legal-and-compliance): unmodified-client hosting and each end user's own direct billing are conditions; the authentication section separately restricts third-party subscription routing and directs developers to provider clarification.
- [Claude plan/SDK guidance](https://support.claude.com/en/articles/15036540-use-the-claude-agent-sdk-with-your-claude-plan): the October 7 update explicitly describes continued CLI/SDK/third-party usage against subscription limits. This supports personal programmatic use; the legal product/authentication restrictions are documented separately.
- [Claude CLI](https://code.claude.com/docs/en/cli-reference), [headless operation](https://code.claude.com/docs/en/headless): technical controls and authentication diagnostics. `--bare` is unsuitable here because it disables native OAuth/keychain authentication.

## Verified locally

Codex `0.163.0-alpha.10.2` and Claude Code `2.1.308` are installed on this Mac. Generated Codex JSON schemas confirmed named `permissions` profiles, resolved-profile provenance and native image-result bytes. A direct native sandbox test rejected reading a canary outside the private working folder. No native credential files were read or copied.

The opt-in `scripts/native-e2e.mjs --images` passed real Codex subscription execution through WDC: explicit consent, signed/encrypted progressive output, two-turn context, JSON-schema validation with a supplied document, image generation/editing, authenticated chunk downloads/uploads, actual execution metadata and cancellation. The `--claude` variant also passed existing native subscription authentication, explicit consent, encrypted incremental output, two-turn context, schema validation with a supplied document, truthful native model/version metadata, omission switches and cancellation. Compatibility checks run without model calls. Standard tests use fixtures and deliberately disabled native execution.

## Remaining boundaries

- Commercial/hosted Codex subscription use needs a separate supported provider route. A loopback origin alone cannot establish that an app is noncommercial; the integration must meet provider conditions too.
- Claude local invocation is enabled at the project owner's explicit request. Publication/distribution conditions remain a separate maintainer responsibility: the CLI and subscription support documentation do not establish blanket rights to offer subscription routing as a hosted service. Personal login and isolated native controls are required at runtime; managed policies are never bypassed.
- Native Linux/Windows execution/sandbox testing remains release validation. Signing/notarization is an optional publisher credential step; independent security review is recommended, not a platform requirement. Version/managed-policy incompatibilities fail closed.
- App tools and Workspace use an explicit WDC dispatcher with runtime isolation checks. File transport is opaque; direct native-format support remains client-specific.
- Codex may retain a native generated-image cache copy. WDC consumes returned PNG bytes only; clearing WDC history cannot promise to erase native-client storage.

See [native execution](native-execution.md) for the enforced controls, formats, storage limits and version-sensitive interface details. Provider restrictions are not bypassable by paired requests or web settings.
