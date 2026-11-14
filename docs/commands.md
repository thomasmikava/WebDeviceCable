# Configured commands

WebDeviceCable creates `config.yaml` in its private data directory on first launch. The Commands page shows its path. Edit this file with a local editor and restart the runner. It is never rewritten by an HTTP endpoint. An invalid or unsupported configuration stops startup and is preserved for repair; no commands execute while startup fails.

Codex and Claude Code have built-in native integrations using existing subscription logins; image generation/editing is Codex-only. Custom commands do not enable those adapters, establish provider eligibility, or provide Restricted-mode isolation. Direct custom actions named `codex`/`claude`, or invoking those executable names, are refused. Arbitrary scripts are trusted local code; their behavior and provider/service eligibility are the configuration author's responsibility. This bridge never extracts provider credentials or substitutes API billing.

## Configuration

```yaml
bridge:
  allowWebCommandEditing: false

actions:
  - id: greet
    title: Say hello
    exec: [my-tool, "--message", "{{ .Arguments.message }}"]
    timeout: 30
    maxConcurrent: 1
    arguments:
      - name: message
        title: Message
        type: ascii_sentence
        default: Hello
        rejectNull: true
```

`exec` is an argument vector: the first element selects an installed executable, the rest become separate arguments. No implicit shell, word splitting, wildcard expansion or command substitution occurs. Placeholder values are inserted exactly once into their existing argument. This prevents shell interpolation by the bridge; it cannot make every possible program/argument combination safe. A configuration that explicitly invokes a shell, interpreter or script still gives that program its normal powers. Executable lookup uses the runner's PATH; launch via CLI or use an absolute path if a desktop launch cannot find a program.

## OliveTin compatibility boundary

The supported action/argument fields retain the shapes documented by [OliveTin](https://github.com/OliveTin/OliveTin/blob/main/config.yaml) and its [exec interface](https://docs.olivetin.app/action_execution/shellvsexec.html). This is an independent implementation; no OliveTin code is bundled.

| Field | Supported behavior |
| --- | --- |
| `id`, `title` | Unique command ID and name. An omitted ID becomes a stable hash of its title. Use an explicit ID for website integrations. Built-in IDs are reserved. |
| `exec` | Program plus up to 31 arguments; at most 8 KiB of definition text. Program cannot be a placeholder. |
| `arguments` | Up to 16 named inputs; `name`, `title`, `type`, `default`, `rejectNull`, `choices` supported. |
| `choices` | Up to 32 `{value, title?}` entries. Values must match a configured choice; the type validator is bypassed when choices exist. |
| `type` | `ascii`, `ascii_identifier`, `ascii_sentence`, `int`, `url`, `very_dangerous_raw_string`, `raw_string_multiline`. Default: `ascii`. |
| `timeout` | Execution limit, default 3 seconds, range 1–600. The effective owner request maximum also applies, starting when execution begins. Approval/queue waiting is separately bounded to 10 minutes. |
| `maxConcurrent` | Per-command limit, default 1, range 1–4; runner-wide limit of 4 active jobs. Excess jobs wait in order where their command has capacity. |

`ascii` allows letters/digits; `ascii_identifier` also allows `_`, `-`, `.`; `ascii_sentence` also allows spaces and commas. `int` accepts nonnegative decimal digits; `url` requires an HTTP(S) URL with a host. Raw types accept arbitrary UTF-8 except NUL. Empty inputs are allowed unless `rejectNull` is true; choices must always match. Each value is limited to 2 KiB, combined values/defaults to 4 KiB. Unknown submitted inputs are rejected.

Both `{{ .Arguments.message }}` and the older `{{ message }}` placeholders work. This is a bounded substitution interface, not the full Go template engine. `.Env`, entity fields, template functions, shell commands, dashboards, schedules, triggers, ACL definitions, aliases/anchors, multiple YAML documents and all other unsupported fields are rejected explicitly. Do not import an entire OliveTin configuration assuming those policies will be applied. Limits/defaults above describe this implementation, not a complete reproduction of OliveTin semantics.

## Web editing

`bridge.allowWebCommandEditing` defaults to `false` and is read only at startup. Changing the file while the runner is active does not change the gate; restart it. When false, command creation/update/deletion endpoints return `command_editing_disabled`, and the editor is hidden.

When true, only the authenticated local control panel can create/edit/remove web-managed definitions. They are atomically persisted as private `actions.json`; config-file commands stay read-only. No HTTP route edits `config.yaml`, changes the flag, or accepts a storage path. Disabling the flag again locks editing after restart; existing web-managed definitions remain available under their existing grants. Paired websites never gain command-editing authority.

The managed file and main configuration must be regular, bounded files, not symlinks. On Unix they must not be group/world writable. Windows relies on the data directory’s private ACL; Go’s synthesized POSIX mode bits do not represent Windows access rights, and ACL validation still needs native Windows testing. Up to 64 commands exist across both sources. Duplicate IDs/names and unknown fields are errors.

## Consent and execution

Configuration makes an action available for granting; it does not grant access to any website. In Connections, select Command access, choose commands and explicitly acknowledge OS-account permissions. Each grant binds an action's normalized definition hash. The control request includes the hash actually displayed, preventing a stale review from granting a newer definition. Per-job approval remains the default; automatic execution requires its own existing consent.

Commands run with the runner user's filesystem/network rights. They are not OS-sandboxed, and argument validation is not file isolation. Native Restricted/App tools/Workspace promises are not silently extended to these programs. Every job has a private temporary working directory, but this does not prevent access elsewhere. An allowlisted environment excludes ambient provider API keys and overrides; configured programs can still use local resources accessible to the OS account.

Changing/removing a command cancels its live jobs and makes previous grants stale. Review access again before submitting new work. Reviewing grants cancels that pairing's outstanding command jobs, without replaying them. File edits take effect after restart, which interrupts pending jobs. Cosmetic definition edits also change the hash, conservatively requiring renewed grants.

Standard output and standard error are combined and delivered as signed/encrypted full snapshots. The runner drains output continuously, retains up to `bridge.maxTextBytes` (default 1 MiB), normalizes UTF-8, removes terminal control characters and reports `outputTruncated`. Optional binary file transfer is described below. Output-limit exhaustion is an explicit failure. Inputs are erased on terminal transitions; output remains until history is cleared. A nonzero exit code or start failure produces `failed`; successful exit produces `completed` with `exitCode: 0`.

Tab lease expiry, cancellation, revocation, pause, deadlines and normal runner shutdown cancel owned processes. Unix uses process groups; Windows starts suspended and assigns a kill-on-close Job Object before resuming, failing closed if ownership fails. These are lifecycle controls, not a sandbox. Unix programs deliberately detaching from the group, or an abruptly killed/crashed Unix runner, can leave descendants running. Windows Job Objects do not prevent every out-of-band process-launch mechanism. Windows/Linux runtime validation and independent security review remain release work; current process tests run on macOS and packages cross-compile for all targets.

## Website SDK

```ts
const { actions } = await bridge.actions(); // only this pairing's granted actions
const command = actions.find(action => action.id === 'greet' && action.available);
if (!command) throw new Error('Review command access in WebDeviceCable.');

await bridge.connect(); // one lease per requesting tab
const job = await bridge.submit({
  kind: 'command', action: 'greet', arguments: { message: 'Hello' },
  idempotencyKey: crypto.randomUUID(),
});
for await (const update of bridge.stream(job.id)) render(update.output);
```

The website supplies an action ID and validated input values, never an executable or command definition. `actions()` omits executable paths. `available: false` means the definition changed and needs renewed local consent. All requests, chunks, errors and completion use the existing protocol v2 transport. Job idempotency still prevents duplicate execution; cancellation/reconnect never resubmits a process.

## Continuing commands (WDC extension)

`version` and `session` extend the OliveTin-compatible subset; they are not OliveTin configuration promises. One-shot commands keep their existing behavior. A command must deliberately implement this interface; WDC does not infer interactive support from an arbitrary CLI.

```yaml
actions:
  - id: my-session
    title: My local program
    exec: ["/absolute/path/to/program", "--wdc-jsonl"]
    version: "1.2.3" # optional owner-provided metadata, never detected/invented
    timeout: 60
    maxConcurrent: 2
    session:
      protocol: jsonl
      idleTimeout: 60
```

Session `exec` arguments must be static. Each approved turn arrives on standard input as one UTF-8 JSON line: `{"v":1,"type":"turn","id":"<job ID>","prompt":"Hello","arguments":{}}`. Named arguments use the same configured input validation/defaults as one-shot actions. Inputs never become shell text or argv interpolation in this mode.

Write only protocol frames to stdout (one JSON object per line, maximum 64 KiB):

```json
{"v":1,"type":"output","id":"<same job ID>","delta":"Partial response"}
{"v":1,"type":"done","id":"<same job ID>"}
```

Alternatively end with `{"v":1,"type":"error","id":"<same job ID>","error":{"code":"rate_limited","message":"Original service message","raw":"Optional diagnostic"}}`. Recognized codes are `rate_limited`, `context_limit`, `authentication_required`, `permission_denied`, `unavailable`; unknown codes normalize to `command_failed`. The runner supplies a friendly common message and keeps original details in optional diagnostics. Error frames end the session. Stderr is bounded diagnostics for the current turn, not streamed user output. Unknown fields, wrong IDs/versions, malformed/oversized frames, output between turns and early EOF fail closed.

The process remains alive between turns, with its own private working directory; a successful turn has no process exit code because the process continues. Output uses the same configurable text limit per turn. Idle timeout is 0–600 seconds (default 0 follows the tab lease); owned idle processes count toward limits. Input, cancellation, lease and route rules are detailed in [SDK usage](sdk.md). No provider credentials or native-AI eligibility bypass is added by this protocol. The development-only [session worker](../examples/session-worker.mjs) demonstrates it without AI.


## File transfers

Only owner-defined YAML actions can enable this option; the web editor cannot grant it:

```yaml
actions:
  - id: convert
    title: Convert file
    exec: ["/absolute/path/to/your-program"]
    timeout: 120
    files: true
```

For one-shot commands, `WDC_FILES` is JSON containing `inputs:[{id,name,path,mediaType,size}]` and `outputDirectory`. `WDC_OUTPUT_DIR` also supplies that directory. Inputs are staged unchanged in private per-job folders. For session commands, each JSONL `turn` instead contains the same object in its optional `files` field, with separate per-turn directories. Programs should use those explicit paths, without assuming the original filename is unique.

Write finished outputs into `outputDirectory` before exiting successfully or sending the session `done` frame. WDC exports up to 16 regular files (8 MiB each), rejects subdirectories/links/escapes, and returns signed artifact metadata and encrypted byte chunks through the existing SDK. It does not interpret file formats. Outputs are retained within the existing 64 MiB artifact budget until history deletion; staged files are removed when their process/session ends. `files` participates in the pinned command-definition hash, so enabling it requires renewed grants. Commands still use full OS-account permissions; file staging does not sandbox the executable.
