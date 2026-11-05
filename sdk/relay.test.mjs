import {test} from 'node:test';
import assert from 'node:assert/strict';
import {RelayClient, RelayAdminClient, relayRequest, BridgeError, relayConnectURL, relaySettingsURL} from '../internal/bridge/web/relay.js';

test('older relay count caps require an update before large catalogs install handlers or submit work',async()=>{
  const saved=globalThis.fetch,seen=[];let failActions=true;
  globalThis.fetch=async(url,init)=>{
    if(url.endsWith('/relay/v1/capabilities'))return Response.json({scopedToolsVersion:1,limits:{toolsPerExecution:32}});
    const packet=JSON.parse(init.body);seen.push(packet.path);
    if(packet.path==='/v2/actions'&&failActions){failActions=false;throw new TypeError('Temporary network failure');}
    if(packet.path==='/v2/capabilities')return Response.json({scopedToolsVersion:1,pairing:{deviceId:'device',appId:'writer',appUser:'alice',origin:'https://writer.example'}});
    if(packet.path==='/v2/lease')return Response.json({lease:'lease',expiresAt:new Date(Date.now()+60000).toISOString()});
    return Response.json({groups:[]});
  };
  const client=new RelayClient({baseURL:'https://relay.example',apiKey:'fixture-key',connectionId:'a'.repeat(64),account:{appId:'writer',appUser:'alice'},origin:'https://writer.example'});
  const tools=Array.from({length:33},(_,i)=>({id:'tool_'+i,title:'Tool',description:'Fixture',inputSchema:{type:'object',properties:{},required:[],additionalProperties:false},execute:()=>assert.fail('Unsupported tools ran')}));
  try {
    await client.connect();
    await assert.rejects(client.actions(),error=>error.code==='connection_failed');
    for(const execute of [()=>client.preflight({kind:'action',tools}),()=>client.openSession({tools}),()=>client.submit({kind:'action',idempotencyKey:'relay-legacy-count',tools})])await assert.rejects(execute(),error=>error.code==='relay_update_required');
    assert.equal(client.executions.size,0);
    assert.equal(seen.some(path=>['/v2/admission','/v2/jobs','/v2/conversations','/v2/tool-calls/poll'].includes(path)),false);
  } finally {await client.close();globalThis.fetch=saved;}
});

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


test('relay enrollment carries owner-review suggestions and rejects invalid values before sending',async()=>{
 const saved=globalThis.fetch;const seen=[];
 globalThis.fetch=async(url,init)=>{if(url.endsWith('/capabilities'))return Response.json({connectionReviewVersion:1});seen.push({url,body:JSON.parse(init.body)});return Response.json({code:'a'.repeat(64)});};
 const options={baseURL:'https://relay.example',apiKey:'fixture-key',appUser:'alice',logoURL:'https://app.example/logo.svg',requestedPermissions:{automaticApprovals:true}};
 try {
  await RelayClient.enroll(options);
  assert.deepEqual(seen[0].body,{appUser:'alice',logoURL:options.logoURL,requestedPermissions:{automaticApprovals:true}});
  for(const logoURL of ['file:///etc/passwd','http://169.254.169.254/','https://user:password@app.example/logo','https://app.example/logo#fragment'])assert.throws(()=>RelayClient.enroll({...options,logoURL}),error=>error.code==='invalid_logo');
  assert.throws(()=>RelayClient.enroll({...options,requestedPermissions:{automaticApprovals:'yes'}}),error=>error.code==='invalid_permissions');
  assert.throws(()=>RelayClient.enroll({...options,requestedPermissions:{workspace:true}}),error=>error.code==='invalid_permissions');
  assert.equal(seen.length,1);
 }finally {globalThis.fetch=saved;}
});


test('connection suggestions require a current relay before creating an enrollment',async()=>{
 const saved=globalThis.fetch;const seen=[];
 try {
  for(const result of [()=>Response.json({scopedToolsVersion:1}),()=>Response.json({error:{code:'not_found'}},{status:404})]){
   globalThis.fetch=async url=>{seen.push(url);return result();};
   await assert.rejects(RelayClient.enroll({baseURL:'https://relay.example',apiKey:'fixture',appUser:'alice',requestedPermissions:{automaticApprovals:true}}),error=>error.code==='relay_update_required');
  }
  assert.equal(seen.length,2);assert.ok(seen.every(url=>url.endsWith('/capabilities')));
 } finally {globalThis.fetch=saved;}
});

test('relay feature gates reject new input schemas and operation reporting before submission',async()=>{
 const saved=globalThis.fetch,seen=[];
 globalThis.fetch=async(url,init)=>{
  if(url.endsWith('/relay/v1/capabilities'))return Response.json({scopedToolsVersion:1});
  const packet=JSON.parse(init.body);seen.push(packet.path);
  if(packet.path==='/v2/capabilities')return Response.json({scopedToolsVersion:1,toolInputSchemaVersion:1,toolOperationDetailsVersion:1,pairing:{deviceId:'device',appId:'writer',appUser:'alice',origin:'https://writer.example'}});
  if(packet.path==='/v2/lease')return Response.json({lease:'lease',expiresAt:new Date(Date.now()+60000).toISOString()});
  return Response.json({});
 };
 const client=new RelayClient({baseURL:'https://relay.example',apiKey:'fixture',connectionId:'a'.repeat(64),account:{appId:'writer',appUser:'alice'},origin:'https://writer.example'});
 try{
  await client.connect();
  for(const definition of [{inputSchema:{type:'object',additionalProperties:true}},{inputSchema:{type:'object',properties:{},required:[],additionalProperties:false},operationDetails:true}]){
   const tools=[{id:'dispatch',title:'Dispatch',description:'Fixture',execute:()=>assert.fail('Unsupported work ran'),...definition}];
   await assert.rejects(client.preflight({kind:'action',tools}),error=>error.code==='relay_update_required');
   await assert.rejects(client.submit({kind:'action',idempotencyKey:'new-relay-features',tools}),error=>error.code==='relay_update_required');
  }
  assert.equal(client.executions.size,0);assert.equal(seen.some(path=>['/v2/admission','/v2/jobs','/v2/tool-calls/poll'].includes(path)),false);
 }finally{await client.close();globalThis.fetch=saved;}
});
