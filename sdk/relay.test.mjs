import {test} from 'node:test';
import assert from 'node:assert/strict';
import {RelayClient, RelayAdminClient, relayRequest, BridgeError} from '../internal/bridge/web/relay.js';

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
