# Security requirements

## Modes and consent
- **Restricted (default):** supplied inputs/output artifacts only; no shell, general filesystem access or application tools.
- **App tools:** expose registered app callbacks; their app owns authorization and confirmations. WDC validates schemas, isolates tabs/accounts and prevents duplicate dispatch.
- **Workspace:** selected folders through WDC’s rooted file dispatcher; native shell stays disabled. Separate **Commands** mode runs configured executables with explicit OS-account permissions, without a Restricted-mode isolation claim.
- Runner and native controls enforce scopes independently of prompts/UI; unsupported modes fail closed.
- Pairings default to approval per job; automation requires consent. Command grants pin definition hashes; edits require renewed grants and cancel affected work.
- Each direct tab or owned backend operation maintains its own authenticated lease; expiry cancels only its jobs. The control panel need not stay open.

## Pairing and local data
- Bind revocable app signing keys to app ID, exact origin, device and app user; keep both sides' private keys separate and never expose provider tokens.
- Trusted control-panel invitations pin the runner key; verify signed ephemeral channels before encrypted claim, authenticate request nonces and chained stream chunks, and re-pair after key loss.
- Users inspect scopes, approve app-initiated pairing in the authenticated local panel, pause/revoke access, end leased sessions and erase terminal history; routing never grants commands.
- Isolate app sessions, files, configuration and tools; never inherit unrelated hooks/integrations.
- Treat prompts, attachments and tool results as untrusted; they cannot grant permissions.
- Desktop listener and administration bind only to loopback; enforce exact Host, authenticated API access and browser Origin checks.
- Bundle assets locally; origin-scoped control sessions and CSRF headers protect writes. Command editing requires a file-only startup flag; the web UI never writes config.yaml and paired apps cannot edit commands.
- Use restrictive local storage permissions, bounded request sizes, atomic persistence and one writer per data directory; updates require pinned release signatures, owner-only controls and drained work.
- Minimize content/logging; never log credentials; redact native-client diagnostic output and erase terminal prompts.
- Keep provider authentication in unmodified official clients; never read/copy credential files or use undocumented inference endpoints.
- Reject API-key-only authentication rather than silently charging an API account.
- Personal subscription privacy settings apply; Azure protections do not carry over.

## Provider eligibility
- Native-client reuse is distinct from Sign in with ChatGPT/Claude integrations; verify its own conditions.
- Local/open-source Codex app-server authentication is documented; commercial/hosted services need a separate supported route.
- Invoke unmodified Claude -p with the user's native sign-in; disable inherited hooks/tools and never intermediate provider credentials.
- Codex image output uses validated native PNG bytes; ignore returned filesystem paths. Its separate native cache is retained.
- Review [Codex app-server](https://learn.chatgpt.com/docs/app-server#auth-endpoints), [Codex images](https://learn.chatgpt.com/docs/image-generation), [Claude conditions](https://code.claude.com/docs/en/legal-and-compliance) and [SIWC terms](https://openai.com/policies/sign-in-with-chatgpt-terms/).
