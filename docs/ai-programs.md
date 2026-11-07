# AI programs

Settings → **AI programs** lists the installations available on this computer.
First run creates one automatically detected Codex entry and one Claude Code
entry. **Add AI program** creates another named entry, such as Work Codex,
Personal Claude or Local Qwen. Commands remain in their separate Commands area.

## Installations and profiles

Each entry has a stable ID independent of its display name, executable and
profile directory. Renaming preserves connection selections and conversations.
Connections → **Program settings** selects an entry for the application's
default, any workflow, or any stage. Model and reasoning settings inherit
independently, using that entry's checked model catalog.

For Codex and Claude Code, an empty executable uses automatic detection. Set an
absolute executable path to select another installation. Native integrations
manage their own safety arguments; additional arguments are available only for
Custom integrations. An optional absolute profile directory sets `CODEX_HOME`
for Codex or `CLAUDE_CONFIG_DIR` for Claude Code. WDC does not create native
sign-ins, copy credentials or sign in on your behalf. Use that client's own
login flow with the same profile configuration first.

On Windows, automatic Codex detection also checks the desktop app's native CLI
under `%LOCALAPPDATA%\OpenAI\Codex\bin`. WDC selects the most recently installed
runtime containing `codex.exe` when Codex is absent from PATH. A CLI on PATH or
an explicitly configured executable still takes precedence.

Claude detection also checks the desktop app's bundled CLI cache, including
Windows Store installations. A standalone CLI in the user bin or on PATH takes
precedence. Detecting the desktop binary does not transfer the desktop app's
login: WDC uses the CLI's own `auth status` result for the configured profile.
If the Code tab is signed in but the CLI reports no login, sign in using that
CLI's `auth login` command. WDC does not read or copy desktop session tokens.

**Check connection** invokes that entry's executable and profile without
inference. It reports installation, compatibility, version, models and account
identity when supplied. Sign-in and native safety checks are per entry. A missing
installation or unsupported capability produces a blocking error; WDC never
chooses another program or account automatically.

The default Codex and Claude entries are editable. **Reset to automatic
detection** clears executable/profile overrides when saved. Removing a used
entry lists affected connections and selections. Reassign them first or confirm
that they should remain unavailable. Removed default entries can be restored
explicitly in Settings; this preserves their original compatibility IDs.

## Account and conversation boundaries

Existing `codex` and `claude` selections and account bindings migrate unchanged.
New entries use generated IDs. Each connection pins accounts separately by
program ID. Switching accounts requires disconnecting and reconnecting affected
applications; editing a name, route or profile never clears account approval.

Changing an entry's executable, arguments, integration type or profile ends its
queued/running requests and conversations with `program_changed`. Requests pin a
configuration hash before dispatch. Continuing conversations cannot pick up a
new configuration. Renaming alone changes none of these boundaries. Process
restarts still close native conversations; consumed requests are never replayed.

Native execution metadata keeps the actual integration in `provider`, the entry
in `programId`, and its display name in `name`. Custom results use `kind: custom`
and `billing: program_defined`; WDC does not claim a native subscription or invent
a provider/model/usage. Local sharing choices apply to program names and IDs.
Executable paths, profiles, diagnostic account identities and grants stay local.

## Custom interface: `wdc-ai-jsonl-v1`

Custom executables must implement this interface directly or through a wrapper.
Registering an arbitrary CLI does not make it compatible. This first version
supports text, incremental output, optional continuing turns, and optional
reported model/reasoning catalogs, and optional supplied files. A descriptor
with `files: true` opts into the existing `files` manifest (original name, media
type, size and private staged path; plus an output directory). No extension or
content-type allowlist is applied to custom file inputs. Files are rejected
before dispatch when the descriptor omits that declaration, and support is
checked again before every turn. JSON output schemas, image generation and app
tools remain unsupported and are rejected before dispatch.

Each connection must grant the **current configuration** in **Custom AI program
access**, accepting the same OS account file/network permissions used by
Commands. Editing executable, arguments, type or profile invalidates that grant.
The harness has the user's filesystem and network permissions: capabilities are
feature declarations, not sandbox guarantees. Do not register an untrusted
executable. No shell is inserted; configured arguments are passed literally.
General-purpose command grants are independent and unchanged.

WDC passes an allowlisted environment without ambient API tokens or provider
endpoint overrides. The optional profile directory is `WDC_PROFILE_DIR` for a
custom executable. The wrapper owns its authentication and configuration. It may
use its profile to load its own credentials; credentials must never appear in
the protocol. Every process is owned, bounded and cancelled with its request or
connection lease, using the existing command process/session machinery.

### Descriptor

WDC runs the configured executable and arguments with `--wdc-info` appended.
Exit successfully within three seconds and emit one JSON object to stdout:

```json
{
  "protocol": "wdc-ai-jsonl-v1",
  "version": "1.0.0",
  "account": "stable-non-secret-account-id",
  "sessions": true,
  "models": [{
    "model": "qwen-local",
    "displayName": "Local Qwen",
    "supportedReasoningEfforts": [{"reasoningEffort": "high"}]
  }]
}
```

Only `protocol` is required; `sessions` defaults to false. Keep descriptor stdout
within 4096 bytes. Write diagnostics to stderr. `version`, `account` and `models`
are optional. Only reported model/reasoning choices are selectable. The account
must change when the effective account changes and must be checked against the
actual sign-in, not a hardcoded label. WDC rechecks it before every turn and pins
it per connection/entry. With no account identity WDC reports verification as
unavailable; it can pin only the program configuration. Custom identity reports
are trusted declarations from the executable, unlike the dedicated native
integrations' account checks.

### Turns

WDC starts the same executable/arguments with `--wdc-session` appended, in a
private temporary working directory. Read UTF-8 JSON objects, one per stdin line:

```json
{"v":1,"type":"turn","id":"request-id","prompt":"Hello","model":"qwen-local","reasoning":"high"}
```

`model` and `reasoning` are omitted for client defaults. Send stdout frames with
the same `id` and `v: 1`:

```json
{"v":1,"type":"output","id":"request-id","delta":"Hello!"}
{"v":1,"type":"done","id":"request-id"}
```

Or finish with an error:

```json
{"v":1,"type":"error","id":"request-id","error":{"code":"unavailable","message":"Local service stopped"}}
```

Supported error codes include `rate_limited`, `context_limit`,
`authentication_required`, `permission_denied` and `unavailable`; others become
`command_failed`. Do not send frames after completion except for a new turn.
Malformed, unsolicited or mismatched frames end the session. Maximum frame size
is 64 KiB and total text output follows WDC's configured limit (1 MiB by default).
Individual requests remain bounded to 600 seconds and the active lease. With
`sessions: true`, keep history in memory across turns in that process; do not
reuse another process's history. One-shot requests end the process immediately
after completion. Continuing sessions end with their lease or after 60 seconds
idle.

Applications use the existing browser or relay SDK group APIs. `provider` in a
request/capability is the stable local program ID; most integrations should let
the user choose it through groups. Capabilities include `integration`,
`isolation`, `streaming` and `sessions` alongside existing feature flags. A later
endpoint integration can use the same naming, IDs and route hierarchy.
