import {test} from 'node:test';
import assert from 'node:assert/strict';
import {RelayClient, RelayAdminClient, relayRequest, BridgeError, relayConnectURL, relaySettingsURL} from '../internal/bridge/web/relay.js';

test('relay requests preserve Headers and tuple headers while retaining project authentication', async () => {
  const saved = globalThis.fetch;
  const seen = [];
  globalThis.fetch = async (url, init) => {
    seen.push(url);
    const headers = new Headers(init.headers);
    assert.equal(headers.get('X-WDC-Account'), 'alice');
    assert.equal(headers.get('Authorization'), 'Bearer fixture-key');
    assert.equal(init.redirect, 'error');
    return Response.json({ok:true});
  };
  try {
    for (const headers of [new Headers({'X-WDC-Account':'alice', Authorization:'wrong'}), [['X-WDC-Account','alice']]]) {
      assert.deepEqual(await relayRequest({baseURL:'https://relay.example',apiKey:'fixture-key'}, '/fixture', {headers}), {ok:true});
    }
    assert.equal(seen.length, 2);
  } finally { globalThis.fetch = saved; }
});

test('a non-JSON proxy failure becomes a structured error without leaking its body', async () => {
  const saved = globalThis.fetch;
  globalThis.fetch = async () => new Response('<h1>private proxy diagnostics</h1>', {status:502});
  try {
    await assert.rejects(RelayClient.enroll({baseURL:'https://relay.example',apiKey:'fixture-key',appUser:'alice'}), error =>
      error instanceof BridgeError && error.code === 'invalid_response' && error.status === 502 && !error.message.includes('private'));
  } finally { globalThis.fetch = saved; }
});

test('invalid connection IDs and non-loopback HTTP endpoints are rejected before network access', () => {
  for (const connectionId of ['../other', '', 'not-a-connection']) {
    assert.throws(() => RelayClient.revoke({baseURL:'https://relay.example',apiKey:'fixture-key',connectionId,appUser:'alice'}));
  }
  for (const baseURL of ['http://relay.example', 'https://user:password@relay.example', 'https://relay.example/path']) {
    assert.throws(() => new RelayAdminClient({baseURL,adminKey:'fixture-key'}));
  }
});

test('relay app links contain a single-use invitation or connection ID, never the project credential', () => {
  const code='a'.repeat(64), connectionId='b'.repeat(64);
  const invitation=new URL(relayConnectURL({baseURL:'https://relay.example',code}));
  assert.equal(invitation.host,'relay-connect'); assert.equal(invitation.searchParams.get('relayUrl'),'https://relay.example'); assert.equal(invitation.searchParams.get('code'),code);
  const settings=new URL(relaySettingsURL({baseURL:'https://relay.example',connectionId}));
  assert.equal(settings.host,'settings');assert.equal(settings.searchParams.get('connectionId'),connectionId);
  assert.throws(()=>relayConnectURL({baseURL:'http://192.168.1.1',code}));
  assert.throws(()=>relayConnectURL({baseURL:'http://localhost:47323',code}));
  assert.throws(()=>relaySettingsURL({baseURL:'https://relay.example',connectionId:'../other'}));
});

test('old relay capability endpoints fail clearly before any desktop submission',async()=>{
  const saved=globalThis.fetch;const seen=[];
  try{
    for(const response of [()=>Response.json({error:{code:'not_found'}},{status:404}),()=>Response.json({})]){
      globalThis.fetch=async url=>{seen.push(url);assert.ok(url.endsWith('/relay/v1/capabilities'));return response();};
      const client=new RelayClient({baseURL:'https://relay.example',apiKey:'fixture-key',connectionId:'a'.repeat(64),account:{appId:'writer',appUser:'alice'},origin:'https://writer.example'});
      await assert.rejects(client.run({prompt:'Do not submit'}),error=>error.code==='relay_update_required');
      await client.close();
    }
    assert.equal(seen.length,2);
  }finally{globalThis.fetch=saved;}
});
