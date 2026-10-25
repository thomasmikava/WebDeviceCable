import {test} from 'node:test';
import assert from 'node:assert/strict';
import {indexedDB} from 'fake-indexeddb';
import {ConnectionStore} from '../internal/bridge/web/index.js';
const origin='https://writer.example';
test('saved pairings preserve non-extractable CryptoKeys and stay scoped to app, account and origin', async()=>{
  globalThis.indexedDB=indexedDB;
  const keys=await crypto.subtle.generateKey({name:'ECDSA',namedCurve:'P-256'},false,['sign','verify']);
  const connection={pairingId:'a'.repeat(64),identity:{deviceId:'b'.repeat(64),publicKey:'pinned',endpoint:'http://127.0.0.1:47321'},privateKey:keys.privateKey,publicKey:'app'};
  const alice=new ConnectionStore({origin,appId:'writer',appUser:'alice'});
  await alice.save(connection);
  const restored=await new ConnectionStore({origin,appId:'writer',appUser:'alice'}).load();
  assert.equal(restored.privateKey.extractable,false);
  const message=new TextEncoder().encode('restored pairing');
  const signature=await crypto.subtle.sign({name:'ECDSA',hash:'SHA-256'},restored.privateKey,message);
  assert.ok(await crypto.subtle.verify({name:'ECDSA',hash:'SHA-256'},keys.publicKey,signature,message));
  await assert.rejects(crypto.subtle.exportKey('jwk',restored.privateKey));
  for(const scope of [{origin,appId:'writer',appUser:'bob'},{origin,appId:'other',appUser:'alice'},{origin:'https://other.example',appId:'writer',appUser:'alice'}]) assert.equal(await new ConnectionStore(scope).load(),undefined);
  await alice.clear(); assert.equal(await alice.load(),undefined);
  const extractable=await crypto.subtle.generateKey({name:'ECDSA',namedCurve:'P-256'},true,['sign','verify']);
  await assert.rejects(alice.save({...connection,privateKey:extractable.privateKey}),error=>error.code==='invalid_connection');
});
test('unavailable browser storage reports a recoverable error without a weaker key fallback',async()=>{
  delete globalThis.indexedDB;
  await assert.rejects(new ConnectionStore({origin,appId:'writer',appUser:'alice'}).load(),error=>error.code==='storage_unavailable');
});
