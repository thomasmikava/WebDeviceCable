import {testDirectory, testPort, testRuntime} from './test-instance.mjs';
import { existsSync } from 'node:fs';
// Development test: real binary + real HTTP + compiled SDK. Never calls providers.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { readFile, rm, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { BridgeClient } from '../internal/bridge/web/index.js';
import { SecureTransport } from '../internal/bridge/web/secure.js';
const dir = await testDirectory('e2e'), origin = 'http://127.0.0.1:47322';
let child, base, sessionID, csrf, port = testPort(); const clients = [];
const delay = ms => new Promise(resolve => setTimeout(resolve,ms));
async function boot() {
  child = spawn(resolve(process.env.WDC_BINARY ?? (existsSync(process.platform==='win32'?'dist/WebDeviceCable.exe':'dist/WebDeviceCable') ? (process.platform==='win32'?'dist/WebDeviceCable.exe':'dist/WebDeviceCable') : (process.platform==='win32'?'dist/webdevicecable.exe':'dist/webdevicecable'))), ['serve','--data-dir',dir,'--port',port], {stdio:'pipe'});
  for (let i = 0; i < 100; i++) {
    try { base = await testRuntime(dir,child); break; } catch { await delay(50); }
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
  await boot();
  const alias = `http://localhost:${new URL(base).port}`;
  for (const method of ['GET','HEAD']) {
    const response = await fetch(alias+'/connections?tab=apps&name=a%20b',{method,redirect:'manual'});
    assert.equal(response.status,307); assert.equal(response.headers.get('location'),base+'/connections?tab=apps&name=a%20b');
  }
  for (const [method,path] of [['GET','/api'],['HEAD','/api/state'],['POST','/']]) {
    assert.equal((await fetch(alias+path,{method,redirect:'manual'})).status,403);
  }
  const first = await pair('alice'); const secondTab = new BridgeClient({baseURL:base,connection:first.connection,origin}); clients.push(secondTab); await secondTab.connect();
  const pendingInvite = await api('/invitations',{appId:'writer.example',name:'Writer',origin,appUser:'alice'});
  const pendingID = pendingInvite.pairing.id;
  const pendingList = await api('/invitations',undefined,'GET');
  assert.ok(pendingList.invitations.some(invite=>invite.id===pendingID && invite.manual));
  assert.ok(!JSON.stringify(pendingList).includes(pendingInvite.invitation));
  assert.ok(!JSON.stringify(pendingList).includes('secretHash'));
  const anotherPanel = await fetch(base+'/api/session',{method:'POST',headers:{Origin:base,Authorization:'Bearer '+await readFile(dir+'/admin.key','utf8')}}).then(response=>response.json());
  const otherHeaders = {Origin:base,'X-Bridge-Session':anotherPanel.session,'X-Bridge-CSRF':anotherPanel.csrf};
  assert.equal((await fetch(base+'/api/invitations',{headers:{Origin:base}})).status,401);
  assert.equal((await fetch(base+'/api/invitations/'+pendingID,{method:'DELETE',headers:{Origin:base,'X-Bridge-Session':anotherPanel.session}})).status,403);
  const otherList = await fetch(base+'/api/invitations',{headers:otherHeaders}).then(response=>response.json());
  assert.ok(otherList.invitations.some(invite=>invite.id===pendingID));
  assert.equal((await fetch(base+'/api/invitations/'+pendingID,{method:'DELETE',headers:otherHeaders})).status,200);
  assert.ok(!(await api('/invitations',undefined,'GET')).invitations.some(invite=>invite.id===pendingID));
  await assert.rejects(BridgeClient.pair({baseURL:base,origin,invitation:pendingInvite.invitation,appId:'writer.example',appUser:'alice'}));
  const routingMessage = 'Choose a default program for this app in WebDeviceCable, under Connections.';
  const unrouted = {kind:'action',prompt:'Fixture request; must never execute',idempotencyKey:'http-routing-refusal'};
  for (let i=0;i<3;i++) await assert.rejects(first.client.submit(unrouted),error=>error.status===409&&error.code==='routing_required'&&error.message===routingMessage);
  const failures = (await api('/state',undefined,'GET')).jobs.filter(job=>job.code==='routing_required');
  assert.equal(failures.length,1); assert.equal(failures[0].status,'failed'); assert.equal(failures[0].pairingId,first.pairing.id);
  assert.equal(failures[0].detail,routingMessage); assert.equal(failures[0].error.message,routingMessage); assert.equal(failures[0].request.prompt,undefined);
  assert.equal(await first.client.lookup(unrouted.idempotencyKey),null);
  await api('/history',undefined,'DELETE');
  await assert.rejects(first.client.submit(unrouted),error=>error.code==='routing_required');
  assert.equal((await api('/state',undefined,'GET')).jobs.length,0);
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
  // History opt-out uses the unchanged SDK stream/lookup/retry paths.
  await api('/history',{keep:false,confirmDiscard:true},'PATCH');
  const privateRequest={kind:'connection-test',idempotencyKey:'http-history-private'};
  const privateJob=await reconnected.submit(privateRequest);
  assert.ok((await api('/state',undefined,'GET')).jobs.some(job=>job.id===privateJob.id));
  await api(`/jobs/${privateJob.id}/decision`,{approve:true});
  for await(const job of reconnected.stream(privateJob.id))final=job;
  assert.equal(final.status,'completed');assert.match(final.output,/No AI provider was called/);
  assert.ok(!(await api('/state',undefined,'GET')).jobs.some(job=>job.id===privateJob.id));
  assert.ok(!(await readFile(dir+'/state.json','utf8')).includes(privateJob.id));
  await assert.rejects(reconnected.lookup(privateRequest.idempotencyKey),error=>error.status===410&&error.code==='history_deleted');
  await assert.rejects(reconnected.submit(privateRequest),error=>error.status===410&&error.code==='history_deleted');
  await api(`/pairings/${other.pairing.id}`,{requestHistory:'keep'},'PATCH');
  const kept=await reconnected.submit({kind:'connection-test',idempotencyKey:'http-history-override'});
  await api(`/jobs/${kept.id}/decision`,{approve:false});
  for await(const job of reconnected.stream(kept.id))final=job;
  assert.equal(final.code,'user_declined');
  assert.ok((await api('/state',undefined,'GET')).jobs.some(job=>job.id===kept.id));
  await api(`/pairings/${other.pairing.id}`,{requestHistory:'discard',confirmDiscard:true},'PATCH');
  assert.ok(!(await api('/state',undefined,'GET')).jobs.some(job=>job.id===kept.id));
  console.log('E2E passed: localhost redirects, bounded routing-refusal history, mutually signed/encrypted HTTP, streamed progress before completion, pairing, consent, tab close/expiry, isolation, provider gate, revocation and persistent pins after crash restart. No model requests.');
} finally { for (const client of clients) await client.close().catch(() => {}); await stop(); await rm(dir,{recursive:true,force:true}); }
