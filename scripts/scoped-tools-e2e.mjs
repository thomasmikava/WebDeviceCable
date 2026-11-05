// Disposable desktop + local relay. --native=codex|claude explicitly spends
// native subscription quota; the default proves encrypted HTTP admission only.
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {once} from 'node:events';
import {mkdtemp,mkdir,readFile,writeFile,rm} from 'node:fs/promises';
import {createServer} from 'node:net';
import {resolve} from 'node:path';
import {BridgeClient} from '../internal/bridge/web/index.js';
import {RelayClient,RelayAdminClient} from '../internal/bridge/web/relay.js';

const provider=process.argv.find(arg=>arg.startsWith('--native='))?.split('=')[1];
assert.ok(!provider||['codex','claude'].includes(provider));
const delay=ms=>new Promise(resolve=>setTimeout(resolve,ms));
const root=resolve('.cache/tmp');await mkdir(root,{recursive:true});
const dir=await mkdtemp(root+'/scoped-tools-');const children=[],clients=[];
let base,control;
async function api(path,body,method='POST'){
  const response=await fetch(base+'/api'+path,{method,headers:{Origin:base,'Content-Type':'application/json','X-Bridge-Session':control.session,'X-Bridge-CSRF':control.csrf},...(body===undefined?{}:{body:JSON.stringify(body)})});
  const result=await response.json();assert.ok(response.ok,JSON.stringify(result));return result;
}
async function port(){const server=createServer();await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));const value=server.address().port;await new Promise(resolve=>server.close(resolve));return value;}
function launch(binary,args,env=process.env){const child=spawn(resolve(binary),args,{env,stdio:['ignore','pipe','pipe']});children.push(child);let diagnostic='';child.stderr.on('data',data=>{diagnostic=(diagnostic+data).slice(-10000)});child.on('error',error=>{diagnostic=error.message});child.diagnostic=()=>diagnostic;return child;}
const catalog=(count,label,property,seen)=>Array.from({length:count},(_,index)=>({
  id:index?'unused_'+index:'scope_echo',title:index?'Unused fixture '+index:'Scope echo',readOnly:true,
  description:(index?'Unused test fixture; do not call. ':'Verification tool: invoke this tool to obtain a secret random token. The token is available only in its result. ')+('Description padding for a realistic catalog. '.repeat(32)),
  inputSchema:{type:'object',properties:{[property]:{type:'string'}},required:[property],additionalProperties:false},
  execute:(args,context)=>{assert.equal(typeof args[property],'string');assert.ok(context.jobId);assert.ok(context.requestIdempotencyKey);assert.match(context.toolSetHash,/^[a-f0-9]{64}$/);const marker=label+'-'+crypto.randomUUID();seen.push({label,marker,args,context});return {marker,value:args[property]};},
}));

try{
  await mkdir(dir+'/desktop');await writeFile(dir+'/desktop/config.yaml','bridge:\n  disableNative: '+!provider+'\nactions: []\n');
  const desktop=launch(process.env.WDC_BINARY??'.cache/bin/wdc-scoped',['serve','--data-dir',dir+'/desktop','--port','0']);
  for(let i=0;i<200;i++){try{base=JSON.parse(await readFile(dir+'/desktop/runner.json','utf8')).url;break}catch{}if(desktop.exitCode!==null)throw Error(desktop.diagnostic());await delay(50);}
  assert.ok(base,desktop.diagnostic());
  control=await fetch(base+'/api/session',{method:'POST',headers:{Origin:base,Authorization:'Bearer '+await readFile(dir+'/desktop/admin.key','utf8')}}).then(response=>response.json());
  if(provider){
    let state;for(let i=0;i<300;i++){state=await api('/state',undefined,'GET');if(!state.nativeChecking&&state.native.length)break;await delay(100);}
    assert.ok(state.native.find(program=>program.id===provider)?.execution,'UNMET PROVIDER: '+JSON.stringify(state.native));
  }
  const relayPort=await port(),relayURL='http://127.0.0.1:'+relayPort,adminKey='a'.repeat(64);
  await writeFile(dir+'/relay.key',adminKey,{mode:0o600});
  const relay=launch(process.env.WDC_RELAY_BINARY??'.cache/bin/wdc-relay-scoped',['--data-dir',dir+'/relay','--listen','127.0.0.1:'+relayPort],{...process.env,WDC_ADMIN_KEY_FILE:dir+'/relay.key'});
  for(let i=0;i<200;i++){try{if((await fetch(relayURL+'/readyz')).ok)break}catch{}if(relay.exitCode!==null)throw Error(relay.diagnostic());await delay(50);}
  const origin='https://scoped-tools.example';
  const admin=new RelayAdminClient({baseURL:relayURL,adminKey});
  const project=await admin.createProject({appId:'scoped-tools',name:'Scoped tools QA',origin});
  const enrollment=await RelayClient.enroll({baseURL:relayURL,apiKey:project.apiKey,appUser:'owner'});
  const expected=await api('/relay/inspect',{url:relayURL,code:enrollment.code});
  await api('/relay/connect',{url:relayURL,code:enrollment.code,expected});
  const backend=new RelayClient({baseURL:relayURL,apiKey:project.apiKey,connectionId:enrollment.connectionId,account:{appId:'scoped-tools',appUser:'owner'},origin});clients.push(backend);
  for(let i=0;i<100;i++){if((await backend.status()).connected)break;await delay(50);}
  await backend.connect();
  const invitation=await api('/invitations',{appId:'scoped-tools-direct',name:'Scoped tools direct QA',appUser:'owner',origin,automatic:false});
  const paired=await BridgeClient.pair({origin,baseURL:base,invitation:invitation.invitation,appId:'scoped-tools-direct',appUser:'owner'});
  const direct=new BridgeClient({origin,connection:paired.connection});clients.push(direct);await direct.connect();
  for(const [transport,client]of [['direct',direct],['relay',backend]]){
    const pairing=(await client.capabilities()).pairing;
    const seen=[],chatTools=catalog(30,transport+'-chat','value',seen),ruleTools=catalog(3,transport+'-rules','rule',seen);
    assert.ok(Buffer.byteLength(JSON.stringify(chatTools))>40960);
    if(!provider){
      const jobs=await Promise.all([client.submit({kind:'text',provider:'codex',prompt:'Pending chat fixture',idempotencyKey:'chat-admission',tools:chatTools}),client.submit({kind:'text',provider:'codex',prompt:'Pending rules fixture',idempotencyKey:'rules-admission',tools:ruleTools})]);
      assert.notEqual(jobs[0].request.toolSetHash,jobs[1].request.toolSetHash);assert.ok(jobs.every(job=>job.status==='awaiting_approval'&&!job.request.tools));
      await client.cancel(jobs[0].id);assert.equal((await client.get(jobs[1].id)).status,'awaiting_approval');
      assert.equal((await client.lookup('chat-admission')).id,jobs[0].id);
      await assert.rejects(client.submit({kind:'text',provider:'codex',prompt:'Pending chat fixture',idempotencyKey:'chat-admission',tools:ruleTools}),error=>error.code==='idempotency_conflict');
      await client.cancel(jobs[1].id);assert.equal(seen.length,0);
      const maximum=Array.from({length:32},(_,index)=>({id:'max_'+index,title:'\0'.repeat(100),description:'\0'.repeat(2000),inputSchema:{type:'object',properties:{},required:[],additionalProperties:false,description:'<'.repeat(8000)},execute:()=>assert.fail('Preflight installed a handler')}));
      const admission=await client.preflight({kind:'text',provider:'codex',prompt:'<'.repeat(32768),tools:maximum});
      assert.equal(admission.available,false);assert.equal(admission.code,'native_disabled');
      console.log(transport,'HTTP 30/3 catalogs, scoped cancellation, receipts and conflict passed');continue;
    }
    await api('/pairings/'+pairing.id,{automatic:true},'PATCH');
    const session=client.session({provider,tools:chatTools});
    const word='saffron-'+crypto.randomUUID().slice(0,8);
    const [chat,rules]=await Promise.all([
      session.run({prompt:`Remember the word ${word}. You MUST invoke the verification app tool with value ${word} to obtain its secret token. Reply with the token from its result and the remembered word. Do not guess the token or skip the call.`,timeoutSeconds:180}),
      client.run({kind:'text',provider,tools:ruleTools,prompt:'You MUST invoke the verification app tool once with rule lavender to obtain its secret random token. Reply only with the marker from its result and its value. Do not guess a token or skip the actual invocation.',timeoutSeconds:180}),
    ]);
    assert.deepEqual(seen.map(call=>call.label).sort(),[transport+'-chat',transport+'-rules'].sort(),JSON.stringify({chat:chat.output,rules:rules.output}));
    assert.ok(chat.output.includes(seen.find(call=>call.label===transport+'-chat').marker));assert.ok(rules.output.includes(seen.find(call=>call.label===transport+'-rules').marker));
    assert.equal(seen.find(call=>call.label.endsWith('-chat')).context.conversationId,session.id);
    assert.equal(seen.find(call=>call.label.endsWith('-rules')).context.conversationId,undefined);
    const recall=await session.run({prompt:'What exact word did I ask you to remember? Reply only with the word. Do not call a tool.',timeoutSeconds:180});assert.match(recall.output,new RegExp(word));
    await session.close();
    const replacement=client.session({provider,tools:catalog(1,transport+'-updated','updated',seen)});
    const final=await replacement.run({prompt:`Saved conversation history: the user asked to remember ${word}; the assistant confirmed ${word}. Current request: repeat that remembered word and invoke the current verification app tool once with updated equal to the word. You MUST obtain and report its secret random token; it cannot be guessed.`,timeoutSeconds:180});
    assert.match(final.output,new RegExp(word));assert.ok(final.output.includes(seen.find(call=>call.label===transport+'-updated')?.marker??'missing-token'));assert.notEqual(replacement.id,session.id);
    assert.equal(seen.length,3);await replacement.close();
    console.log(provider,transport,'native 30/3 isolation, schemas, callbacks, continuing context and explicit history-backed replacement passed');
  }
}finally{
  for(const client of clients)await client.close().catch(()=>{});
  for(const child of children.reverse()){if(child.exitCode===null){const stopped=once(child,'exit');child.kill('SIGTERM');await stopped;}}
  await rm(dir,{recursive:true,force:true});
}
