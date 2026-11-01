# WebDeviceCable privacy

WebDeviceCable runs on your computer. It has no hosted AI relay, advertising or product analytics. Independently paired websites can read the responses they request; encrypted transport does not hide content from the requesting website or its scripts.

The program you select receives the prompt and files needed for that request. Codex and Claude Code use their existing native sign-ins and their providers' personal-account privacy settings. WebDeviceCable never reads, copies or sends provider login credentials, and never silently switches to paid API billing. Configured commands have the OS-account permissions described when you grant access.

Connections, permission grants, command configuration, installation keys, replay receipts and bounded response history are stored in your private OS profile. Terminal prompts are removed. Clearing history removes WebDeviceCable's retained output and files; independently installed clients can retain their own caches. Uninstalling preserves private settings so reinstalling does not lose your connections. See [operations](docs/operations.md) before deleting or restoring state.

Community test installations use manual updates and make no automatic update-service requests. Opening the downloads link visits GitHub in your browser. Trusted publisher-signed installations enable automatic updates by default. While running, WebDeviceCable checks GitHub for stable releases at most daily during normal operation; you can also check manually. Update requests disclose your IP address and ordinary HTTP metadata to GitHub, including the WebDeviceCable version in the user agent. They include no prompts, files, pairing IDs, native account details or provider credentials. Download selection identifies the required OS/architecture. macOS update authorization and publisher validation can also contact Apple; Windows certificate validation can contact certificate authorities. Sparkle's optional system-profile reporting is disabled.

Turn off Automatic updates in Settings to stop automatic checks and installation. Manual checks remain available. Source builds do not contact the official update service unless deliberately built as official distributions. Updates wait for requests and sessions to end and do not modify saved permissions.

Websites using the SDK and selected external programs have their own privacy policies. Do not post private diagnostics or your state directory publicly. Security reporting is described in [SECURITY.md](SECURITY.md).
