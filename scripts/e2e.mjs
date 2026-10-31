import { existsSync } from 'node:fs';
// Development test: real binary + real HTTP + compiled SDK. Never calls providers.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { BridgeClient } from '../internal/bridge/web/index.js';
import { SecureTransport } from '../internal/bridge/web/secure.js';
const baseDir = resolve('.cache/tmp'); await mkdir(baseDir,{recursive:true});
const dir = await mkdtemp(baseDir + '/e2e-'); const origin = 'http://127.0.0.1:47322';
let child, base, sessionID, csrf, port = '0'; const clients = [];
const delay = ms => new Promise(resolve => setTimeout(resolve,ms));
async function boot() {
  child = spawn(resolve(process.env.WDC_BINARY ?? (existsSync(process.platform==='win32'?'dist/WebDeviceCable.exe':'dist/WebDeviceCable') ? (process.platform==='win32'?'dist/WebDeviceCable.exe':'dist/WebDeviceCable') : (process.platform==='win32'?'dist/webdevicecable.exe':'dist/webdevicecable'))), ['serve','--data-dir',dir,'--port',port], {stdio:'pipe'});
  for (let i = 0; i < 100; i++) {
    try { const info = JSON.parse(await readFile(dir+'/runner.json','utf8')); if (info.pid === child.pid) { base = info.url; break; } await delay(50); } catch { await delay(50); }
  }
  assert.ok(base,'runner must start');
  port = new URL(base).port;
  const key = await readFile(dir+'/admin.key','utf8');
  const response = await fetch(base+'/api/session',{method:'POST',headers:{Origin:base,Authorization:`Bearer ${key}`}});
  assert.equal(response.status,200); const control=await response.json(); sessionID=control.session; csrf=control.csrf;
}
async function stop() { if (!child || child.exitCode !== null) return; const stopped = once(child,'exit'); child.kill('SIGTERM'); await stopped; base = undefined; }
async function api(path,body,method = 'POST') {
  const response = await fetch(base+'/api'+path,{method,headers:{Origin:base,'X-Bridge-Session':sessionID,'X-Bridge-CSRF':csrf,'Content-Type':'application/json'},...(body === undefined ? {} : {body:JSON.stringify(body)})});
  const result = await response.json(); assert.ok(response.ok, result.error?.message); return result;
}
async function pair(user) {
  const inv = await api('/invitations',{appId:'writer.example',name:'Writer',origin,appUser:user,mode:'restricted',automatic:false});
  const paired = await BridgeClient.pair({baseURL:base,origin,invitation:inv.invitation,appId:'writer.example',appUser:user});
  const client = new BridgeClient({baseURL:base,connection:paired.connection,origin}); clients.push(client); await client.connect(); return {client,...paired};
}
try {
  await writeFile(dir+'/config.yaml','bridge:\n  disableNative: true\nactions: []\n',{mode:0o600});
  await boot(); const first = await pair('alice'); const secondTab = new BridgeClient({baseURL:base,connection:first.connection,origin}); clients.push(secondTab); await secondTab.connect();
  assert.equal((await fetch(base+'/v1/capabilities', {headers:{Origin:base}})).status,426);
  assert.equal((await fetch(base+'/v2/capabilities', {headers:{Origin:origin}})).status,426);
  const approved = await first.client.submit({kind:'connection-test',idempotencyKey:'http-approved-test'}); assert.equal(approved.status,'awaiting_approval');
  await api(`/jobs/${approved.id}/decision`,{approve:true});
  let final; const updates = [];
  for await (const job of first.client.stream(approved.id)) {
    updates.push(job); final = job;
    if (job.status === 'running') assert.notEqual((await first.client.get(job.id)).status,'completed','streamed progress must arrive before completion');
  }
  assert.ok(updates.some(j => j.status === 'running' && j.output),'expected incremental output before terminal frame');
  assert.equal(final.status,'completed'); assert.match(final.output,/No AI provider was called/); assert.equal(final.billing,'none');
  const a = await first.client.submit({kind:'connection-test',idempotencyKey:'http-first-tab'});
  const b = await secondTab.submit({kind:'connection-test',idempotencyKey:'http-second-tab'});
  await first.client.close(); assert.equal((await secondTab.get(a.id)).status,'cancelled'); assert.equal((await secondTab.get(b.id)).status,'awaiting_approval');
  const other = await pair('bob'); await assert.rejects(other.client.get(b.id), error => error.status === 404);
  const blocked = await secondTab.submit({kind:'text',provider:'codex',prompt:'Hello',idempotencyKey:'http-provider-blocked'});
  await api(`/jobs/${blocked.id}/decision`,{approve:true});
  for await (const job of secondTab.stream(blocked.id)) final = job;
  assert.equal(final.status,'blocked'); assert.equal(final.code,'native_disabled'); assert.equal(final.actualProvider,undefined); assert.equal(final.request.prompt ?? '', '');
  // A lease without a browser/SDK heartbeat really expires over HTTP.
  const transport = new SecureTransport(other.connection.identity,origin,other.connection,other.connection.pairingId);
  const leaseResponse = await transport.fetch('/v2/lease',{method:'POST'}); assert.equal(leaseResponse.status,201); const idleLease = (await leaseResponse.json()).lease;
  const idleResponse = await transport.fetch('/v2/jobs',{method:'POST',body:JSON.stringify({kind:'connection-test',idempotencyKey:'http-real-expiry'})},idleLease); assert.equal(idleResponse.status,202); const idle = await idleResponse.json();
  await delay(12400); assert.equal((await other.client.get(idle.id)).code,'tab_disconnected'); assert.equal((await secondTab.get(b.id)).status,'awaiting_approval');
  await api(`/pairings/${first.pairing.id}`,{revoke:true},'PATCH'); assert.equal((await api('/state',undefined,'GET')).jobs.find(j => j.id === b.id).status,'cancelled'); await assert.rejects(secondTab.get(b.id));
  const pending = await other.client.submit({kind:'connection-test',idempotencyKey:'http-restart'});
  for (const client of clients) await client.close().catch(() => {});
  // Replace the graceful-close terminal state with a live pending job for crash/restart handling.
  await other.client.connect(); const restarted = await other.client.submit({kind:'connection-test',idempotencyKey:'http-restart-pending'});
  const exit = once(child,'exit'); child.kill('SIGKILL'); await exit; base = undefined;
  await boot(); const state = await api('/state',undefined,'GET'); assert.equal(state.jobs.find(j => j.id === restarted.id).status,'interrupted');
  assert.ok(state.jobs.find(j => j.id === pending.id));
  // A saved pin and non-extractable app key still authenticate the same runner after restart.
  const reconnected = new BridgeClient({connection:other.connection,origin}); clients.push(reconnected); await reconnected.connect(); assert.equal((await reconnected.capabilities()).protocolVersion,2);
  console.log('E2E passed: mutually signed/encrypted HTTP, streamed progress before completion, pairing, consent, tab close/expiry, isolation, provider gate, revocation and persistent pins after crash restart. No model requests.');
} finally { for (const client of clients) await client.close().catch(() => {}); await stop(); await rm(dir,{recursive:true,force:true}); }
