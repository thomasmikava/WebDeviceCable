import { test } from 'node:test';
import assert from 'node:assert/strict';
import { BridgeClient, BridgeError, BridgeSecurityError } from '../internal/bridge/web/index.js';

// Independent WebCrypto peer. The HTTP E2E suite also tests this SDK against Go.
const encode = new TextEncoder(), decode = new TextDecoder();
const b64 = bytes => Buffer.from(bytes).toString('base64url');
const bytes = text => Buffer.from(text, 'base64url');
const hex = () => Buffer.from(crypto.getRandomValues(new Uint8Array(32))).toString('hex');
const hash = async data => Buffer.from(await crypto.subtle.digest('SHA-256', data)).toString('hex');
const ecdsa = { name: 'ECDSA', hash: 'SHA-256' };
const keypair = () => crypto.subtle.generateKey({ name: 'ECDSA', namedCurve: 'P-256' }, false, ['sign', 'verify']);
const json = (data, status = 200) => new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } });
const terminal = status => ['completed', 'cancelled', 'blocked', 'timed_out', 'interrupted', 'failed'].includes(status);
async function sign(key, data) {
  const raw = encode.encode(JSON.stringify(data));
  return { payload: b64(raw), signature: b64(await crypto.subtle.sign(ecdsa, key, raw)) };
}
async function derive(shared, salt, id, direction) {
  const key = await crypto.subtle.importKey('raw', shared, 'HKDF', false, ['deriveKey']);
  return crypto.subtle.deriveKey({ name: 'HKDF', hash: 'SHA-256', salt, info: encode.encode(`ai-bridge/v2/${id}/${direction}`) }, key, { name: 'AES-GCM', length: 256 }, false, ['encrypt', 'decrypt']);
}
async function seal(key, id, data) {
  const iv = crypto.getRandomValues(new Uint8Array(12));
  return { sessionId: id, iv: b64(iv), data: b64(await crypto.subtle.encrypt({ name: 'AES-GCM', iv, additionalData: encode.encode(`ai-bridge/v2/${id}/response`) }, key, encode.encode(JSON.stringify(data)))) };
}
async function runner() {
  const keys = await keypair(), publicKey = b64(await crypto.subtle.exportKey('raw', keys.publicKey));
  const origin = 'https://app.example', endpoint = 'http://127.0.0.1:47321', deviceId = hex(), pairingId = hex(), secret = hex(), lease = hex();
  const identity = { publicKey, endpoint, deviceId };
  const invitation = 'aib2.' + b64(encode.encode(JSON.stringify({ ...identity, v: 2, secret, origin, appId: 'writer', appUser: 'alice', expiresAt: Date.now() + 300000 })));
  const pairing = { id: pairingId, deviceId, origin, appId: 'writer', appUser: 'alice', mode: 'restricted', automatic: false };
  const peer = { identity, invitation, origin, secret, lease, calls: [], wire: [], sessions: new Map(), mutateHello: hello => hello, signingKey: keys.privateKey };
  let appKey;
  peer.fetch = async (url, init) => {
    peer.wire.push({ url, init }); assert.equal(init.credentials, 'omit'); assert.equal(init.redirect, 'error'); assert.equal(init.headers.Origin, origin); assert.equal(init.headers.Authorization, undefined);
    const path = new URL(url).pathname;
    if (path === '/v2/channel') {
      const input = JSON.parse(init.body), ephemeral = await crypto.subtle.generateKey({ name: 'ECDH', namedCurve: 'P-256' }, false, ['deriveBits']);
      const remote = await crypto.subtle.importKey('raw', bytes(input.publicKey), { name: 'ECDH', namedCurve: 'P-256' }, false, []);
      const shared = await crypto.subtle.deriveBits({ name: 'ECDH', public: remote }, ephemeral.privateKey, 256), salt = crypto.getRandomValues(new Uint8Array(32)), id = hex();
      peer.sessions.set(id, { receive: await derive(shared, salt, id, 'request'), send: await derive(shared, salt, id, 'response'), used: new Set() });
      const hello = peer.mutateHello({ features:input.features??[], ...identity, v: 2, kind: 'ai-bridge-channel', origin, nonce: input.nonce, clientPublicKey: input.publicKey, serverPublicKey: b64(await crypto.subtle.exportKey('raw', ephemeral.publicKey)), salt: b64(salt), sessionId: id, expiresAt: Date.now() + 900000 });
      return json(await sign(peer.signingKey, hello));
    }
    assert.equal(path, '/v2/request');
    const envelope = JSON.parse(init.body), channel = peer.sessions.get(envelope.sessionId);
    const plaintext = await crypto.subtle.decrypt({ name: 'AES-GCM', iv: bytes(envelope.iv), additionalData: encode.encode(`ai-bridge/v2/${envelope.sessionId}/request`) }, channel.receive, bytes(envelope.data));
    const signed = JSON.parse(decode.decode(plaintext)), raw = bytes(signed.payload), request = JSON.parse(decode.decode(raw));
    const verifier = await crypto.subtle.importKey('raw', bytes(request.publicKey), { name: 'ECDSA', namedCurve: 'P-256' }, false, ['verify']);
    assert.ok(await crypto.subtle.verify(ecdsa, verifier, bytes(signed.signature), raw));
    assert.equal(request.sessionId, envelope.sessionId); assert.equal(request.origin, origin); assert.ok(!channel.used.has(request.nonce)); channel.used.add(request.nonce);
    const requestHash = await hash(raw); let sequence = 0, previous = '';
    const context = { request, init, async frame(kind, body, status = 200, mutate = frame => frame, badSignature = false) {
      const frame = mutate({ v: 2, kind, sessionId: envelope.sessionId, nonce: request.nonce, requestHash, origin, pairingId: request.pairingId, method: request.method, path: request.path, sequence, previous, status, body });
      const signed = await sign(peer.signingKey, frame); previous = await hash(bytes(signed.payload)); sequence++;
      if (badSignature) signed.signature = b64(new Uint8Array(64));
      return seal(channel.send, envelope.sessionId, signed);
    }, async json(body, status = 200, mutate, badSignature) { return json(await this.frame('response', JSON.stringify(body), status, mutate, badSignature)); },
    async sse(jobs, options = {}) {
      const chunks = [];
      for (const job of jobs) chunks.push(`event: bridge\ndata: ${JSON.stringify(await this.frame(terminal(job.status) ? 'end' : 'event', JSON.stringify(job), 200, options.mutate, options.badSignature))}\n\n`);
      return new Response((options.chunks ? options.chunks(chunks) : chunks).join(''), { headers: { 'Content-Type': 'text/event-stream' } });
    } };
    peer.calls.push(context);
    if (request.path === '/v2/channel/current') return context.json({ok:true});
    if (request.path === '/v2/pairings/claim') {
      assert.equal(request.authorization, secret); assert.equal(request.pairingId, ''); appKey = request.publicKey;
      return context.json({ pairing }, 201);
    }
    assert.equal(request.publicKey, appKey); assert.equal(request.pairingId, pairingId); assert.equal(request.authorization, undefined);
    if (peer.handler) { const response = await peer.handler(context); if (response) return response; }
    if (request.path === '/v2/capabilities')return context.json({scopedToolsVersion:1,protocolVersion:2,pairing,routingVersion:2});
    if (request.path === '/v2/lease') return context.json({ lease, expiresAt: new Date(Date.now() + 12000).toISOString() }, request.method === 'POST' ? 201 : 200);
    if (request.path === '/v2/jobs') {
      assert.equal(request.lease, lease);
      const {tools:_tools,...execution}=JSON.parse(request.body);
      return context.json({ id: 'job', status: 'queued', revision: 1, request:{...execution,toolSetHash:'hash'} }, 202);
    }
    if (request.path.endsWith('/events')) return context.sse([{ id: 'job', revision: 2, status: 'completed', output: 'Two chunks.' }]);
    return context.json({ id: 'job', revision: 3, status: 'cancelled' });
  };
  peer.install = () => { const saved = globalThis.fetch; globalThis.fetch = peer.fetch; return () => { globalThis.fetch = saved; }; };
  peer.pair = () => BridgeClient.pair({ invitation, appId: 'writer', appUser: 'alice', origin });
  peer.connect = async () => { const paired = await peer.pair(); const client = new BridgeClient({ connection: paired.connection, origin }); await client.connect(); return { ...paired, client }; };
  return peer;
}
const collect = async iterator => { const values = []; for await (const value of iterator) values.push(value); return values; };

test('pairing pins identities, uses non-extractable keys, and preserves the signed SDK lifecycle', async () => {
  const peer = await runner(), restore = peer.install(); let client;
  try {
    const paired = await peer.pair(); assert.equal(paired.connection.identity.publicKey, peer.identity.publicKey);
    assert.equal(paired.connection.privateKey.extractable, false);
    await assert.rejects(crypto.subtle.exportKey('pkcs8', paired.connection.privateKey));
    assert.equal(structuredClone(paired.connection).privateKey.extractable, false);
    client = new BridgeClient({ connection: paired.connection, origin: peer.origin });
    await assert.rejects(client.submit({ kind: 'connection-test', idempotencyKey: 'test-key' }), BridgeError);
    await Promise.all([client.connect(), client.connect()]);
    assert.equal(peer.calls.filter(c => c.request.path === '/v2/lease').length, 1);
    await client.submit({ kind: 'text', prompt: 'Private test prompt', idempotencyKey: 'test-key' });
    let streams = 0;
    peer.handler = c => c.request.path.endsWith('/events') ? c.sse([{ id: 'job', revision: ++streams, status: streams === 1 ? 'running' : 'completed', output: 'Incremental output' }]) : undefined;
    assert.deepEqual((await collect(client.stream('job'))).map(j => j.status), ['running', 'completed']);
    assert.equal(streams, 2); assert.equal(peer.calls.filter(c => c.request.path === '/v2/jobs').length, 1);
    const streamCalls = peer.calls.filter(c => c.request.path.endsWith('/events')); assert.notEqual(streamCalls[0].request.nonce, streamCalls[1].request.nonce);
    await client.cancel('job'); await client.close();
    assert.equal(peer.calls.at(-1).request.method, 'DELETE'); assert.equal(peer.calls.at(-1).init.keepalive, true);
    await assert.rejects(client.submit({ kind: 'connection-test', idempotencyKey: 'test-key' }), /connect/);
    for (const call of peer.wire) { assert.ok(!call.init.body.includes(peer.secret)); assert.ok(!call.init.body.includes('Private test prompt')); }
  } finally { await client?.close().catch(() => {}); restore(); }
});

test('rejects untrusted endpoint addresses, old invitations, and account mismatches', async () => {
  const peer = await runner(), restore = peer.install();
  try {
    const { connection } = await peer.pair();
    for (const baseURL of ['https://evil.example', 'http://localhost:47321', 'http://127.0.0.1:47321/path', 'http://127.0.0.1:47321?token=a', 'http://user:pass@127.0.0.1:47321', 'http://127.0.0.1:47323']) assert.throws(() => new BridgeClient({ baseURL, connection, origin: peer.origin }));
    assert.throws(() => new BridgeClient({ connection: { ...connection, privateKey: {} }, origin: peer.origin }));
    await assert.rejects(BridgeClient.pair({ invitation: 'a'.repeat(64), origin: peer.origin, appId: 'writer', appUser: 'alice' }), BridgeSecurityError);
    await assert.rejects(BridgeClient.pair({ invitation: peer.invitation, origin: peer.origin, appId: 'writer', appUser: 'bob' }), /match/);
    assert.equal(peer.calls.filter(call => call.request.path !== '/v2/channel/current').length, 1);
  } finally { restore(); }
});

test('an impersonating runner or substituted handshake cannot receive the invitation', async t => {
  for (const mutation of ['key', 'nonce', 'clientPublicKey', 'origin', 'endpoint', 'deviceId', 'expiresAt']) await t.test(mutation, async () => {
    const peer = await runner(), restore = peer.install();
    try {
      if (mutation === 'key') peer.signingKey = (await keypair()).privateKey;
      else peer.mutateHello = hello => ({ ...hello, [mutation]: mutation === 'expiresAt' ? Date.now() - 1000 : hex() });
      await assert.rejects(peer.pair(), BridgeSecurityError);
      assert.equal(peer.calls.length, 0); assert.equal(peer.wire.length, 1); assert.ok(!peer.wire[0].init.body.includes(peer.secret));
    } finally { restore(); }
  });
});

test('accepts verified errors and rejects tampering and replayed responses', async t => {
  const peer = await runner(), restore = peer.install();
  try {
    const { connection } = await peer.pair(), client = new BridgeClient({ connection, origin: peer.origin });
    peer.handler = c => c.json({ error: { code: 'pairing_revoked', message: 'Revoked' } }, 401);
    await assert.rejects(client.capabilities(), e => e instanceof BridgeError && e.code === 'pairing_revoked' && e.status === 401);
    await t.test('signature independent of transport encryption', async () => {
      peer.handler = c => c.json({}, 200, undefined, true); await assert.rejects(client.capabilities(), BridgeSecurityError);
    });
    for (const field of ['nonce', 'requestHash', 'origin', 'pairingId', 'method', 'path', 'sessionId', 'sequence']) await t.test(field, async () => {
      peer.handler = c => c.json({}, 200, frame => ({ ...frame, [field]: field === 'sequence' ? 1 : 'substituted' }));
      await assert.rejects(client.capabilities(), BridgeSecurityError);
    });
    await t.test('ciphertext tag', async () => {
      peer.handler = async c => { const response = await c.json({}), envelope = await response.json(); const data = bytes(envelope.data); data[0] ^= 1; return json({ ...envelope, data: b64(data) }); };
      await assert.rejects(client.capabilities(), BridgeSecurityError);
    });
    await t.test('replayed response to a new request', async () => {
      let saved; peer.handler = async c => { if (!saved) saved = await (await c.json({})).json(); return json(saved); };
      await client.capabilities(); await assert.rejects(client.capabilities(), BridgeSecurityError);
    });
    await t.test('unbounded response', async () => {
      peer.handler = () => new Response(' '.repeat(256 * 1024 + 1)); await assert.rejects(client.capabilities(), BridgeSecurityError);
    });
  } finally { restore(); }
});

test('stream rejects forged, reordered, duplicated and missing chunks without retrying security failures', async t => {
  const jobs = [{ id: 'job', revision: 1, status: 'running' }, { id: 'job', revision: 2, status: 'running' }, { id: 'job', revision: 3, status: 'completed' }];
  for (const attack of ['signature', 'sequence', 'chain', 'duplicate', 'remove', 'reorder', 'unsigned', 'terminal']) await t.test(attack, async () => {
    const peer = await runner(), restore = peer.install(); let client;
    try {
      ({ client } = await peer.connect()); let streams = 0;
      peer.handler = c => {
        if (!c.request.path.endsWith('/events')) return;
        streams++;
        if (attack === 'unsigned') return new Response('event: job\ndata: {"status":"completed"}\n\n', { headers: { 'Content-Type': 'text/event-stream' } });
        return c.sse(jobs, {
          badSignature: attack === 'signature',
          mutate: frame => ({ ...frame, ...(attack === 'sequence' ? { sequence: 4 } : {}), ...(attack === 'chain' ? { previous: 'bad' } : {}), ...(attack === 'terminal' && frame.kind === 'end' ? { kind: 'event' } : {}) }),
          chunks: chunks => attack === 'duplicate' ? [chunks[0], ...chunks] : attack === 'remove' ? [chunks[0], chunks[2]] : attack === 'reorder' ? chunks.reverse() : chunks,
        });
      };
      await assert.rejects(collect(client.stream('job')), BridgeSecurityError); assert.equal(streams, 1);
    } finally { await client?.close().catch(() => {}); restore(); }
  });
});

test('fails immediately on unsigned stream HTTP errors rather than silently retrying', async () => {
  const peer = await runner(), restore = peer.install(); let client;
  try {
    ({ client } = await peer.connect()); let streams = 0;
    peer.handler = c => { if (c.request.path.endsWith('/events')) { streams++; return json({ error: 'fake endpoint' }, 401); } };
    await assert.rejects(collect(client.stream('job')), BridgeSecurityError); assert.equal(streams, 1);
  } finally { await client?.close().catch(() => {}); restore(); }
});

test('lists granted commands and verifies a failed command terminal frame with its exit code', async () => {
  const peer = await runner(), restore = peer.install(); let client;
  try {
    ({ client } = await peer.connect());
    peer.handler = c => {
      if (c.request.path === '/v2/actions') return c.json({actions:[{id:'convert',title:'Convert',available:true,arguments:[],timeout:30}]});
      if (c.request.path.endsWith('/events')) return c.sse([{id:'job',revision:1,status:'failed',exitCode:7,output:'Configured command failed'}]);
    };
    assert.equal((await client.actions()).actions[0].id,'convert');
    const jobs = await collect(client.stream('job')); assert.equal(jobs.length,1); assert.equal(jobs[0].exitCode,7);
  } finally { await client?.close().catch(() => {}); restore(); }
});

test('nested and standalone typed groups serialize simply and run() uses the local choice', async () => {
 const peer = await runner(), restore = peer.install(); let client;
 try {
  const {connection} = await peer.pair();
  const groups = [{id:'ai',name:'AI',kind:'ai',children:[{id:'chat',name:'Chat',kind:'ai'}]},{id:'export',name:'Export'}];
  const received=[];
  peer.handler = c => {
   if(c.request.path === '/v2/groups'){const body=JSON.parse(c.request.body);received.push(body.groups);return c.json({groups:body.groups});}
   if(c.request.path === '/v2/jobs'){const body=JSON.parse(c.request.body);assert.equal(body.kind,'action');assert.equal(body.group,'ai.chat');assert.ok(body.idempotencyKey.length>=8);return c.json({id:'job',revision:1,status:'queued'},202);}
  };
  client = new BridgeClient({connection,origin:peer.origin,groups});
  const result=await client.run({group:'ai.chat',prompt:'Hello'});
  assert.equal(result.status,'completed');
  assert.deepEqual(received[0],[{id:'ai',name:'AI',kind:'ai'},{id:'ai.chat',name:'Chat',kind:'ai',parentId:'ai'},{id:'export',name:'Export'}]);
  assert.equal(received.length,1);await client.connect();assert.equal(received.length,1);
 }finally{await client?.close().catch(()=>{});restore();}
});

test('session helpers reuse one context, preserve streaming and expose structured failures', async () => {
 const peer=await runner(),restore=peer.install();let client;
 try{
  ({client}=await peer.connect());let opens=0,turns=0;
  peer.handler=c=>{
   if(c.request.path==='/v2/conversations'){opens++;return c.json({id:'context',status:'open'},201);}
   if(c.request.path==='/v2/conversations/context/turns'){turns++;return c.json({id:'job',status:'queued',revision:1},202);}
   if(c.request.path.endsWith('/events'))return c.sse([{id:'job',status:'running',revision:1,output:'A'},{id:'job',status:'completed',revision:2,output:'AB'}]);
  };
  const session=client.session({group:'ai.chat'});
  assert.equal((await session.run('One')).output,'AB');
  assert.deepEqual((await collect(session.stream('Two'))).map(job=>job.output),['A','AB']);
  assert.equal(opens,1);assert.equal(turns,2);assert.equal(session.id,'context');
  peer.handler=c=>c.request.path.endsWith('/events')?c.sse([{id:'job',status:'failed',revision:3,error:{code:'context_limit',category:'context_limit',message:'Too much context',retryable:false,raw:'safe detail'}}]):c.request.path.endsWith('/turns')?c.json({id:'job',status:'queued'},202):undefined;
  await assert.rejects(session.run('Three'),error=>error.code==='context_limit'&&error.category==='context_limit'&&error.raw==='safe detail'&&error.job.status==='failed');
  await session.close();await assert.rejects(session.send('Four'),error=>error.code==='session_closed');
 }finally{await client?.close().catch(()=>{});restore();}
});

test('an ambiguous turn is never replayed or replaced implicitly, lookup recovers it',async()=>{
 const peer=await runner(),restore=peer.install();let client;
 try{
  ({client}=await peer.connect());let turns=0,lookup=0;
  peer.handler=c=>{
   if(c.request.path==='/v2/conversations')return c.json({id:'context',status:'open'},201);
   if(c.request.path.endsWith('/turns')){turns++;throw new TypeError('network gone after submission');}
   if(c.request.path==='/v2/jobs/lookup'){lookup++;assert.equal(JSON.parse(c.request.body).idempotencyKey,'unknown-turn-key');return c.json({job:{id:'job',status:'completed'}});}
  };
  const session=client.session({action:'worker'});
  await assert.rejects(session.send({prompt:'One',idempotencyKey:'unknown-turn-key'}),e=>e.code==='submission_unknown'&&e.idempotencyKey==='unknown-turn-key');
  await assert.rejects(session.send('Do something else'),e=>e.code==='submission_unknown');assert.equal(turns,1);
  assert.equal((await session.recover()).status,'completed');assert.equal(lookup,1);assert.equal(turns,1);
 }finally{await client?.close().catch(()=>{});restore();}
});

test('an unreadable submission response retains its operation key for recovery', async () => {
  const peer = await runner(), restore = peer.install(); let client;
  try {
    ({client} = await peer.connect());
    peer.handler = async context => {
      if (context.request.path === '/v2/jobs') return json(await context.frame('response', '<html>proxy error</html>', 502));
    };
    await assert.rejects(client.submit({kind:'connection-test', idempotencyKey:'unreadable-response'}), error =>
      error.code === 'submission_unknown' && error.idempotencyKey === 'unreadable-response');
    assert.equal(peer.calls.filter(call=>call.request.path === '/v2/jobs').length, 1);
  } finally { await client?.close().catch(()=>{}); restore(); }
});

test('oversized Unicode tool results return a bounded error instead of stalling the callback', async () => {
  const peer = await runner(), restore = peer.install(); let client, timer, releaseCalls;
  try {
    const paired = await peer.pair();
    let receiveResult;
    const result = new Promise(resolve=>{receiveResult=resolve;});
    const callbacksReady=new Promise(resolve=>{releaseCalls=resolve;});
    peer.handler = async context => {
      if (context.request.path === '/v2/tool-calls/poll') {
        await callbacksReady;
        return context.json({calls:[{id:'unicode-call',name:'read',arguments:{},jobId:'job',requestIdempotencyKey:'unicode-key',toolSetHash:'hash'}]});
      }
      if (context.request.path.endsWith('/claim')) return context.json({ok:true});
      if (context.request.path.endsWith('/result')) { receiveResult(JSON.parse(context.request.body)); return context.json({ok:true}); }
    };
    client = new BridgeClient({connection:paired.connection, origin:peer.origin});
    await client.connect();
    await client.submit({kind:'action',idempotencyKey:'unicode-key',tools:[{
      id:'read',title:'Read',description:'Fixture',inputSchema:{type:'object',properties:{},required:[],additionalProperties:false},execute:()=>({text:'🙂'.repeat(12000)}),
    }]});
    releaseCalls();
    await client.connect();
    const received = await Promise.race([result,new Promise((_,reject)=>{timer=setTimeout(()=>reject(new Error('Tool callback stalled')),4000);})]);
    assert.match(received.result.error,/32 KiB/);
    assert.ok(new TextEncoder().encode(JSON.stringify(received)).length < 32768);
  } finally { releaseCalls?.();clearTimeout(timer); await client?.close().catch(()=>{}); restore(); }
});

test('interactive pairing only accepts its own popup, endpoint, nonce and explicit approval',async()=>{
 const peer=await runner(),restore=peer.install();const saved=globalThis.window;
 const listeners=new Set();let popupURL,posted;
 const popup={closed:false,close(){this.closed=true},postMessage(data,origin){posted={data,origin}}};
 globalThis.window={location:{origin:peer.origin},open(url){popupURL=url;return popup},addEventListener(type,fn){if(type==='message')listeners.add(fn)},removeEventListener(type,fn){listeners.delete(fn)}};
 const emit=event=>{for(const fn of listeners)fn(event)};
 try{
  const pending=BridgeClient.requestPairing({appId:'writer',name:'Writer',appUser:'alice'});
  const requestId=new URLSearchParams(new URL(popupURL).hash.slice(1)).get('connect-request');
  const event={source:popup,origin:peer.identity.endpoint,data:{requestId,type:'wdc:pairing-ready'}};
  emit({...event,source:{}});emit({...event,origin:'https://evil.example'});emit({...event,data:{...event.data,requestId:'bad'}});assert.equal(posted,undefined);
  emit(event);assert.equal(posted.origin,peer.identity.endpoint);assert.equal(posted.data.appId,'writer');assert.equal(posted.data.appUser,'alice');
  emit({...event,data:{requestId,type:'wdc:pairing-approved',invitation:peer.invitation}});
  assert.equal((await pending).pairing.appId,'writer');assert.equal(listeners.size,0);assert.equal(posted.data.type,'wdc:pairing-connected');
  const rejected=BridgeClient.requestPairing({appId:'writer',name:'Writer',appUser:'alice'});
  const nextId=new URLSearchParams(new URL(popupURL).hash.slice(1)).get('connect-request');
  emit({...event,data:{requestId:nextId,type:'wdc:pairing-rejected'}});
  await assert.rejects(rejected,e=>e.code==='pairing_declined');assert.equal(listeners.size,0);
 }finally{globalThis.window=saved;restore();}
});

test('closing during a connection or session opening does not create late work', async()=>{
 const peer=await runner(),restore=peer.install();let client;
 try{
  const {connection}=await peer.pair();client=new BridgeClient({connection,origin:peer.origin});
  let release,opened;const gate=new Promise(resolve=>release=resolve),ready=new Promise(resolve=>opened=resolve);
  peer.handler=async c=>{if(c.request.path==='/v2/lease'&&c.request.method==='POST'){opened();await gate;return c.json({lease:peer.lease,expiresAt:new Date(Date.now()+12000).toISOString()},201)}};
  const connecting=client.connect();await ready;await client.close();release();await assert.rejects(connecting,e=>e.code==='lease_closed');assert.equal(client.connected,false);
  peer.handler=undefined;await client.connect();
  let releaseContext;const contextGate=new Promise(resolve=>releaseContext=resolve);
  client.openSession=async()=>{await contextGate;return{id:'context',status:'open'}};
  const context=client.session({action:'worker'}),sending=context.send('Never send this');
  const closing=context.close();releaseContext();
  await assert.rejects(sending,e=>e.code==='session_closed');await closing;
  assert.equal(peer.calls.filter(c=>c.request.path.endsWith('/turns')).length,0);
 }finally{await client?.close().catch(()=>{});restore();}
});

test('file helpers preserve chunk offsets, verify hashes and reject inconsistent artifact frames',async t=>{
 const peer=await runner(),restore=peer.install();let client;
 try{
  client=new BridgeClient({connection:(await peer.pair()).connection,origin:peer.origin});await client.connect();
  const data=crypto.getRandomValues(new Uint8Array(60000)),sha256=await hash(data),inputId=hex(),artifact={id:hex(),name:'image.png',mediaType:'image/png',size:data.length,sha256};let uploaded=[],offset=0,variant='valid';
  peer.handler=c=>{
   if(c.request.path==='/v2/inputs'){const input=JSON.parse(c.request.body);assert.equal(input.sha256,sha256);assert.equal(input.size,data.length);return c.json({id:inputId,...input},201);}
   if(c.request.path===`/v2/inputs/${inputId}`&&c.request.method==='PUT'){const body=JSON.parse(c.request.body),chunk=Buffer.from(body.data,'base64');assert.equal(body.offset,offset);assert.ok(chunk.length<=24576);uploaded.push(chunk);offset+=chunk.length;return c.json({id:inputId,offset,complete:offset===data.length});}
   if(c.request.path.startsWith(`/v2/jobs/job/artifacts/${artifact.id}/`)){
    const start=Number(c.request.path.split('/').at(-1)),chunk=data.slice(start,start+24576),body={id:artifact.id,offset:start,data:Buffer.from(chunk).toString('base64'),complete:start+chunk.length===data.length};
    if(variant==='offset')body.offset++;if(variant==='id')body.id=hex();if(variant==='hash')body.data=Buffer.alloc(chunk.length).toString('base64');if(variant==='terminal')body.complete=!body.complete;
    return c.json(body);
   }
  };
  const input=await client.upload(new Blob([data],{type:'image/png'}),'reference.png');assert.equal(input.id,inputId);assert.deepEqual(Buffer.concat(uploaded),Buffer.from(data));assert.equal(uploaded.length,3);
  const blob=await client.artifact({id:'job',artifacts:[artifact]});assert.deepEqual(new Uint8Array(await blob.arrayBuffer()),data);
  for(const bad of ['offset','id','hash','terminal'])await t.test(bad,async()=>{variant=bad;await assert.rejects(client.artifact({id:'job',artifacts:[artifact]}),error=>error.code==='verification_failed');});
 }finally{await client?.close().catch(()=>{});restore();}
});


test('saved account binding is verified before opening a lease or registering groups', async () => {
  const peer=await runner(), restore=peer.install();
  try {
    const {connection}=await peer.pair();
    peer.handler=context => context.request.path==='/v2/capabilities' ? context.json({scopedToolsVersion:1,pairing:{appId:'writer',appUser:'alice',origin:peer.origin,deviceId:connection.identity.deviceId}}) : undefined;
    const wrong=new BridgeClient({connection,origin:peer.origin,account:{appId:'writer',appUser:'bob'},groups:{chat:'Chat'}});
    await assert.rejects(wrong.connect(),error=>error.code==='identity_mismatch');
    assert.equal(peer.calls.some(call=>call.request.path==='/v2/lease'),false);
    const right=new BridgeClient({connection,origin:peer.origin,account:{appId:'writer',appUser:'alice'}});
    await right.connect(); await right.close();
  } finally {restore();}
});

test('long-lived transports rotate before nonce capacity and retire old channels', async () => {
  const peer=await runner(),restore=peer.install(); let client;
  try {
    ({client}=await peer.connect());
    for(let i=0;i<1030;i++) await client.get('job');
    const channels=new Set(peer.calls.filter(call=>call.request.path==='/v2/jobs/job').map(call=>call.request.sessionId));
    assert.equal(channels.size,2);
    assert.ok(peer.calls.filter(call=>call.request.path==='/v2/channel/current').length>=2);
    await client.close();
    assert.ok([...peer.sessions.values()].every(channel=>channel.used.size<2048));
  } finally {if(client) await client.close().catch(()=>{}); restore();}
});

test('large Unicode snapshots and ordinary responses reassemble verified bounded fragments',async()=>{
 const peer=await runner(),restore=peer.install();let client;
 try{({client}=await peer.connect());const output='😀日本語\\"\n'.repeat(24000),job={id:'job',revision:8,status:'completed',output};
  peer.handler=async c=>{
   if(c.request.path==='/v2/jobs/job'||c.request.path.endsWith('/events')){
    const chunks=[],body=encode.encode(JSON.stringify(job));
    for(let offset=0;offset<body.length;offset+=16384)chunks.push(`event: bridge\ndata: ${JSON.stringify(await c.frame('fragment',b64(body.subarray(offset,offset+16384))))}\n\n`);
    chunks.push(`event: bridge\ndata: ${JSON.stringify(await c.frame(c.request.path.endsWith('/events')?'end':'response',''))}\n\n`);
    return new Response(chunks.join(''),{headers:{'Content-Type':'text/event-stream'}});
   }
  };
  assert.equal((await client.get('job')).output,output);assert.equal((await collect(client.stream('job')))[0].output,output);
 }finally{await client?.close().catch(()=>{});restore();}
});
test('fragment removal, replay, malformed UTF-8 and unnegotiated framing fail closed',async t=>{
 for(const attack of ['remove','replay','utf8','unnegotiated'])await t.test(attack,async()=>{
  const peer=await runner(),restore=peer.install();let client;
  try{if(attack==='unnegotiated')peer.mutateHello=h=>({...h,features:[]});({client}=await peer.connect());
   peer.handler=async c=>{if(!c.request.path.endsWith('/events'))return;const fragments=[];fragments.push(await c.frame('fragment',b64(new Uint8Array(16384).fill(attack==='utf8'?255:32))));fragments.push(await c.frame('end',JSON.stringify({id:'job',revision:2,status:'completed'})));
    const selected=attack==='remove'?[fragments[1]]:attack==='replay'?[fragments[0],...fragments]:fragments;
    return new Response(selected.map(frame=>`event: bridge\ndata: ${JSON.stringify(frame)}\n\n`).join(''),{headers:{'Content-Type':'text/event-stream'}});
   };
   await assert.rejects(collect(client.stream('job')),BridgeSecurityError);
  }finally{await client?.close().catch(()=>{});restore();}
 });
});
test('app callbacks execute only after a successful claim and are not replayed',async()=>{
 const peer=await runner(),restore=peer.install();let client,calls=0,claimed=false;
 try{({client}=await peer.connect());peer.handler=c=>{
  if(c.request.path==='/v2/tool-calls/poll')return c.json({calls:[{id:'call',name:'create',arguments:{title:'A'},jobId:'job',requestIdempotencyKey:'callback-key',toolSetHash:'hash'}]});
  if(c.request.path.endsWith('/claim')){if(claimed)return c.json({error:{code:'tool_call_consumed',message:'Already claimed'}},409);claimed=true;return c.json({id:'call'});}
  if(c.request.path.endsWith('/result')){assert.deepEqual(JSON.parse(c.request.body).result,{ok:true});return c.json({ok:true});}
 };
 await client.submit({kind:'action',idempotencyKey:'callback-key',tools:[{id:'create',title:'Create',description:'Create task',inputSchema:{type:'object',properties:{},required:[],additionalProperties:false},execute:(args,context)=>{calls++;assert.equal(context.idempotencyKey,'call');assert.deepEqual(args,{title:'A'});return {ok:true};}}]});
 for(let attempt=0;attempt<100&&calls<1;attempt++)await new Promise(resolve=>setTimeout(resolve,10));
 await client.drainTools();await client.drainTools();assert.equal(calls,1);
 }finally{await client?.close().catch(()=>{});restore();}
});

test('permission helper waits for WDC review and reuses remembered access',async()=>{
 const peer=await runner(),restore=peer.install();let client,reads=0,requests=0;
 try{({client}=await peer.connect());peer.handler=c=>{
  if(c.request.path==='/v2/permissions'&&c.request.method==='POST'){requests++;return c.json(requests===1?{id:'review',status:'pending'}:{id:'',status:'completed'});}
  if(c.request.path==='/v2/permissions/review'){reads++;return c.json({id:'review',status:reads===1?'pending':'completed'});}
 };
 assert.equal((await client.requestPermissions({workspace:true})).status,'completed');assert.equal(reads,2);
 assert.equal((await client.requestPermissions({workspace:true})).status,'completed');assert.equal(reads,2);
 }finally{await client?.close().catch(()=>{});restore();}
});

test('a signed update interruption never resubmits a job and reconnects with the original pin',async()=>{
  const peer=await runner(),restore=peer.install();let client;
  try{
    const paired=await peer.pair();client=new BridgeClient({connection:paired.connection,origin:peer.origin});await client.connect();
    peer.handler=c=>c.request.path==='/v2/jobs'?c.json({error:{code:'bridge_updating',category:'unavailable',message:'WebDeviceCable is updating.',retryable:true}},503):undefined;
    await assert.rejects(client.submit({kind:'text',prompt:'keep private',idempotencyKey:'update-once'}),error=>error.code==='bridge_updating'&&error.retryable===true);
    assert.equal(peer.calls.filter(c=>c.request.path==='/v2/jobs').length,1);
    await client.close();peer.handler=undefined;
    const before=peer.wire.filter(c=>new URL(c.url).pathname==='/v2/channel').length;
    await client.connect();assert.ok(peer.wire.filter(c=>new URL(c.url).pathname==='/v2/channel').length>before);
    assert.equal(peer.calls.filter(c=>c.request.path==='/v2/jobs').length,1);
    assert.equal(paired.connection.identity.publicKey,peer.identity.publicKey);
    await client.close();const other=await keypair();peer.signingKey=other.privateKey;
    await assert.rejects(client.connect(),BridgeSecurityError);
  }finally{await client?.close().catch(()=>{});restore();}
});

test('workflow declarations retain native operation hints, recovery aliases and hidden legacy routes', async () => {
  const seen=[];
  const transport={reset(){},close:async()=>{},fetch:async(path,init)=>{seen.push(JSON.parse(init.body));return Response.json({groups:[]});}};
  const client=new BridgeClient({connection:{pairingId:'a'.repeat(64),identity:{deviceId:'b'.repeat(64),publicKey:'',endpoint:'http://127.0.0.1:47321'},privateKey:{},publicKey:''},origin:'https://app.example'},transport);
  await client.registerGroups([{id:'legacy',name:'Legacy',hidden:true},{id:'images',name:'Images',kind:'ai',children:[{id:'render',name:'Render',kind:'ai',operation:'image',replaces:['legacy']}]}]);
  assert.deepEqual(seen[0].groups,[{id:'legacy',name:'Legacy',hidden:true},{id:'images',name:'Images',kind:'ai'},{id:'images.render',name:'Render',operation:'image',replaces:['legacy'],kind:'ai',parentId:'images'}]);
});
