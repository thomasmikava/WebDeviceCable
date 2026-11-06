// Opt-in subscription smoke: keep a Claude callback pending past its heartbeat.
// --images runs a separate Codex image job. All WDC state is disposable.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { mkdtemp, mkdir, readFile, writeFile, rm } from 'node:fs/promises';
import { resolve } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { BridgeClient } from '../internal/bridge/web/index.js';

const images = process.argv.includes('--images');
const root = resolve('.cache/tmp');
const artifacts = resolve('.cache/review/claude-image-session');
await mkdir(root, { recursive: true });
await mkdir(artifacts, { recursive: true });
const dir = await mkdtemp(root + '/claude-progress-');
const origin = 'https://claude-progress.example';
const report = { images, callbacks: 0 };
let child, client, base, auth;

async function api(path, body, method = 'POST') {
  const response = await fetch(base + '/api' + path, {
    method,
    headers: {
      Origin: base,
      'Content-Type': 'application/json',
      'X-Bridge-Session': auth.session,
      'X-Bridge-CSRF': auth.csrf,
    },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  });
  const result = await response.json();
  assert.ok(response.ok, JSON.stringify(result));
  return result;
}

try {
  child = spawn(resolve(process.env.WDC_BINARY ?? '.cache/bin/wdc-claude-progress'), [
    'serve', '--data-dir', dir, '--port', '0',
  ], { stdio: ['ignore', 'pipe', 'pipe'] });
  let diagnostic = '', spawnError;
  child.stdout.resume();
  child.stderr.on('data', data => { diagnostic = (diagnostic + data).slice(-4096); });
  child.on('error', error => { spawnError = error; });
  for (let i = 0; i < 200; i++) {
    try { base = JSON.parse(await readFile(dir + '/runner.json', 'utf8')).url; break; } catch {}
    if (spawnError) throw spawnError;
    if (child.exitCode !== null) throw Error(diagnostic);
    await delay(50);
  }
  assert.ok(base, 'Desktop startup failed');
  auth = await fetch(base + '/api/session', {
    method: 'POST',
    headers: { Origin: base, Authorization: 'Bearer ' + await readFile(dir + '/admin.key', 'utf8') },
  }).then(response => response.json());
  let native;
  for (let i = 0; i < 300; i++) {
    const state = await api('/state', undefined, 'GET');
    if (!state.nativeChecking && state.native?.length) {
      native = state.native.find(program => program.id === 'claude');
      break;
    }
    await delay(100);
  }
  assert.ok(native?.execution, 'UNMET Claude provider: ' + native?.code + ' ' + native?.detail);
  const invitation = await api('/invitations', {
    appId: 'claude-progress', name: 'Claude progress regression', appUser: 'owner', origin, automatic: true, files: images,
  });
  const paired = await BridgeClient.pair({
    origin, baseURL: base, invitation: invitation.invitation, appId: 'claude-progress', appUser: 'owner',
  });
  client = new BridgeClient({
    origin, connection: paired.connection,
    groups: [{ id: 'ai.chat', name: 'Chat', kind: 'ai' }, { id: 'ai.images', name: 'Images', kind: 'ai' }],
  });
  await client.connect();
  await api('/pairings/' + paired.pairing.id, { automatic: true }, 'PATCH');
  await api('/pairings/' + paired.pairing.id + '/routing', {
    expectedRevision: 0,
    routing: { default: { provider: 'claude' }, groups: { 'ai.images': { provider: 'codex' } } },
  }, 'PUT');
  if (images) {
    report.imageAdmission = await client.preflight({
      kind: 'image-generate', group: 'ai.images', mode: 'restricted', prompt: 'Generate exactly one image.',
    }, ['imageGeneration']);
    assert.equal(report.imageAdmission.available, true, JSON.stringify(report.imageAdmission));
    assert.equal(report.imageAdmission.kind, 'native');
  }
  const marker = 'callback-' + crypto.randomUUID();
  const word = 'saffron-' + crypto.randomUUID().slice(0, 8);
  const session = client.session({
    group: 'ai.chat',
    tools: [{
      id: 'generate_image', title: 'Generate a verification image',
      description: 'Required image fixture. Returns a secret marker after generating; wait for its result. Never guess the marker.',
      inputSchema: { type: 'object', properties: {}, required: [], additionalProperties: false },
      execute: async (_args, context) => {
        assert.equal(++report.callbacks, 1, 'Never repeat an uncertain image operation');
        try {
          report.callbackContext = { jobId: context.jobId, conversationId: context.conversationId, toolSetHash: context.toolSetHash };
          const started = Date.now();
          console.log('Claude callback started; waiting past its 30-second heartbeat.');
          if (images) {
            const image = await client.run({
              kind: 'image-generate', group: 'ai.images', timeoutSeconds: 240,
              prompt: 'Generate an image using the built-in image generation tool: a small solid yellow circle on a plain white background.',
            }, context.signal);
            assert.equal(image.execution.provider, 'codex');
            assert.ok(image.artifacts?.length);
            const png = await client.artifact(image);
            assert.equal(png.type, 'image/png');
            assert.ok(png.size > 1000);
            const bytes = Buffer.from(await png.arrayBuffer());
            assert.deepEqual([...bytes.subarray(0, 8)], [137, 80, 78, 71, 13, 10, 26, 10]);
            await writeFile(artifacts + '/generated.png', bytes);
            report.image = { jobId: image.id, status: image.status, provider: image.execution.provider, bytes: png.size };
          }
          await delay(Math.max(0, 45000 - (Date.now() - started)), undefined, { signal: context.signal });
          report.callbackMilliseconds = Date.now() - started;
          report.callbackReturned = true;
          return { marker };
        } catch (error) {
          report.callbackError = { code: error.code, message: error.message, stack: error.stack };
          throw error;
        }
      },
    }],
  });
  const job = await session.send({
    prompt: `Remember ${word}. You must call the generate_image tool exactly once and wait for it to finish. Then reply with its secret marker and the remembered word. Do not call any other tool.`,
    timeoutSeconds: 300,
  });
  let final;
  for await (const update of client.stream(job.id)) final = update;
  const state = JSON.parse(await readFile(dir + '/state.json', 'utf8'));
  const saved = state.jobs.find(item => item.id === job.id);
  report.jobs = state.jobs.map(item => ({ kind: item.request.kind, provider: item.actualProvider, status: item.status, code: item.code, diagnostic: item.diagnostic }));
  Object.assign(report, { status: final.status, code: final.code, diagnostic: saved?.diagnostic, error: saved?.error, output: final.output });
  await writeFile(artifacts + (images ? '/native-images-after.json' : '/native-delay-after.json'), JSON.stringify(report, null, 2));
  assert.equal(final.status, 'completed', JSON.stringify(report));
  assert.equal(report.callbacks, 1);
  assert.equal(report.callbackError, undefined, JSON.stringify(report.callbackError));
  assert.ok(final.output.includes(marker));
  assert.ok(report.callbackMilliseconds >= 45000);
  assert.equal(final.execution.provider, 'claude');
  assert.equal(report.callbackContext.conversationId, session.id);
  const recall = await session.run({
    prompt: 'Repeat only the exact word I asked you to remember. Do not call a tool.', timeoutSeconds: 120,
  });
  assert.ok(recall.output.includes(word));
  report.continuation = 'passed';
  await session.close();
  await writeFile(artifacts + (images ? '/native-images-after.json' : '/native-delay-after.json'), JSON.stringify(report, null, 2));
  console.log(JSON.stringify(report));
} finally {
  await client?.close().catch(() => {});
  if (child?.pid && child.exitCode === null) {
    const stopped = once(child, 'exit');
    child.kill('SIGTERM');
    await stopped;
  }
  await rm(dir, { recursive: true, force: true });
}
