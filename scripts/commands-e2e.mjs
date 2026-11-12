import {testDirectory, testPort, testRuntime} from './test-instance.mjs';
import { existsSync } from 'node:fs';
// Real runner + WebCrypto SDK + development-only Node helper commands. No AI.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { readFile, writeFile, rm } from 'node:fs/promises';
import { resolve } from 'node:path';
import { BridgeClient } from '../internal/bridge/web/index.js';

const dir = await testDirectory('command-e2e'), origin = 'http://127.0.0.1:47322';
const delay = ms => new Promise(resolve => setTimeout(resolve,ms));
const actions = [
  {id:'greet',title:'Say hello',exec:[process.execPath,'-e',"console.log('first');setTimeout(()=>console.log('input:'+process.argv[1]),350);setTimeout(()=>console.log('last'),800)",'{{ .Arguments.message }}'],arguments:[{name:'message',type:'very_dangerous_raw_string',default:'Hello'}],timeout:5},
  {id:'memory',title:'Session worker',version:'example-1',exec:[process.execPath,resolve('examples/session-worker.mjs')],session:{protocol:'jsonl',idleTimeout:60},timeout:5,maxConcurrent:2},
  {id:'long',title:'Long command',exec:[process.execPath,'-e',"console.log('pid:'+process.pid);setTimeout(()=>{},30000)"],timeout:30},
];
const config = enabled => JSON.stringify({bridge:{allowWebCommandEditing:enabled,disableNative:true},actions},null,2);
await writeFile(dir+'/config.yaml',config(false),{mode:0o600});
let child, base, sessionID, csrf, port = testPort(); const clients = [];
async function boot() {
  base = undefined;
  child = spawn(resolve(process.env.WDC_BINARY ?? (existsSync(process.platform==='win32'?'dist/WebDeviceCable.exe':'dist/WebDeviceCable') ? (process.platform==='win32'?'dist/WebDeviceCable.exe':'dist/WebDeviceCable') : (process.platform==='win32'?'dist/webdevicecable.exe':'dist/webdevicecable'))),['serve','--data-dir',dir,'--port',port],{stdio:'pipe'});
  let errors = ''; child.stderr.on('data',data=>{errors+=data});
  for(let i=0;i<100;i++){
    try {base=await testRuntime(dir,child);break}catch{}
    if(child.exitCode!==null)break;await delay(50);
  }
  assert.ok(base,errors || 'runner startup timed out');port=new URL(base).port;
  const key=await readFile(dir+'/admin.key','utf8');
  const response=await fetch(base+'/api/session',{method:'POST',headers:{Origin:base,Authorization:`Bearer ${key}`}});assert.equal(response.status,200);
  const control=await response.json();sessionID=control.session;csrf=control.csrf;
}
async function stop(){if(!child||child.exitCode!==null)return;const stopped=once(child,'exit');child.kill('SIGTERM');await stopped;}
async function api(path,body,method='POST',status=200){
  const response=await fetch(base+'/api'+path,{method,headers:{Origin:base,'X-Bridge-Session':sessionID,'X-Bridge-CSRF':csrf,'Content-Type':'application/json'},...(body===undefined?{}:{body:JSON.stringify(body)})});
  const result=await response.json();assert.equal(response.status,status,JSON.stringify(result));return result;
}
async function grant(pairingID,ids){
  const state=await api('/state',undefined,'GET');const grants=Object.fromEntries(state.commands.filter(c=>ids.includes(c.id)).map(c=>[c.id,c.hash]));
  await api('/pairings/'+pairingID,{actions:grants,commandPermissionsAccepted:true},'PATCH');
}
async function paired(user='alice'){
  const invitation=await api('/invitations',{appId:'writer',name:'Writer',origin,appUser:user},'POST',201);
  const result=await BridgeClient.pair({invitation:invitation.invitation,origin,appId:'writer',appUser:user});
  const client=new BridgeClient({connection:result.connection,origin});clients.push(client);await client.connect();return{...result,client};
}
const collect=async stream=>{let final;for await(const job of stream)final=job;return final};
try{
  await boot();const first=await paired();
  await api('/commands',{id:'extra',title:'Extra',exec:[process.execPath,'--version']},'POST',403);
  assert.deepEqual((await first.client.actions()).actions,[]);
  await assert.rejects(first.client.submit({kind:'command',action:'greet',idempotencyKey:'not-granted'}),error=>error.code==='command_not_granted');
  await grant(first.pairing.id,['greet','long','memory']);assert.equal((await first.client.actions()).actions.length,3);
  const message='literal $(touch nope); & | < > "';
  const submitted=await first.client.submit({kind:'command',action:'greet',arguments:{message},idempotencyKey:'actual-command-stream'});assert.equal(submitted.status,'awaiting_approval');
  await api('/jobs/'+submitted.id+'/decision',{approve:true});
  let progress=false,final;
  for await(const job of first.client.stream(submitted.id)){
    if(job.status==='running'&&job.output){progress=true;assert.notEqual((await first.client.get(job.id)).status,'completed')}
    final=job;
  }
  assert.ok(progress);assert.equal(final.status,'completed');assert.equal(final.exitCode,0);assert.equal(final.output,`first\ninput:${message}\nlast\n`);assert.equal(final.request.arguments,undefined);
  assert.equal((await first.client.submit({kind:'command',action:'greet',arguments:{message},idempotencyKey:'actual-command-stream'})).id,submitted.id);
  await first.client.registerGroups([{id:'ai',name:'AI',kind:'ai',children:[{id:'chat',name:'Chat',kind:'ai'}]},{id:'tools',name:'Tools',children:[{id:'export',name:'Export'}]}]);
  const routing={default:{provider:'codex',model:'native-model-preference',reasoning:'high'},groups:{'ai.chat':{action:'memory'},tools:{action:'greet'}}};
  await api('/pairings/'+first.pairing.id+'/routing',{expectedRevision:0,routing},'PUT');
  const session=first.client.session({group:'ai.chat'});
  async function approvedMessage(input,privacy=false){
    const job=await session.send(input);await api('/jobs/'+job.id+'/decision',{approve:true});let progress=false,result;
    for await(const update of first.client.stream(job.id)){
      if(update.status==='running'&&update.output){progress=true;if(privacy){await api('/sharing',{names:false,versions:false,models:false},'PATCH');privacy=false;}}
      result=update;
    }
    assert.ok(progress);return result;
  }
  const one=await approvedMessage('First');assert.equal(one.execution.versionSource,'configured');assert.equal(one.turn,1);
  const two=await approvedMessage('Second',true);assert.equal(two.turn,2);assert.equal(two.execution,undefined);assert.equal(two.actionTitle,undefined);assert.equal(two.plan,undefined);
  const parsed=job=>JSON.parse(job.output.slice(job.output.indexOf('\n')+1));assert.equal(parsed(one).pid,parsed(two).pid);assert.deepEqual(parsed(two).messages,['First','Second']);
  const same=await first.client.lookup(two.request.idempotencyKey);assert.equal(same.id,two.id);
  const failure=await session.send('/fail');await api('/jobs/'+failure.id+'/decision',{approve:true});const error=await collect(first.client.stream(failure.id));assert.equal(error.error.category,'rate_limit');assert.equal(error.error.retryable,true);assert.equal(error.error.raw,undefined);
  await api('/sharing',{rawErrors:true},'PATCH');assert.ok((await first.client.get(failure.id)).error.raw.includes('[redacted]'));assert.ok(!(await first.client.get(failure.id)).error.raw.includes('sk-example'));
  await api('/sharing',{names:true,versions:true,models:true,rawErrors:false},'PATCH');
  await assert.rejects(session.run('Do not replay'),error=>error.code==='session_closed');
  const routed=await first.client.submit({kind:'action',group:'tools.export',idempotencyKey:'hierarchical-command'});await api('/jobs/'+routed.id+'/decision',{approve:true});assert.equal((await collect(first.client.stream(routed.id))).status,'completed');
  const other=await paired('bob');await assert.rejects(other.client.get(submitted.id),error=>error.status===404);
  const long=await first.client.submit({kind:'command',action:'long',idempotencyKey:'cancel-real-process'});await api('/jobs/'+long.id+'/decision',{approve:true});
  let pid;
  for await(const job of first.client.stream(long.id)){if(job.output?.startsWith('pid:')){pid=Number(job.output.slice(4));break}}
  assert.ok(pid);await first.client.close();
  let stopped=false;for(let i=0;i<60;i++){try{process.kill(pid,0)}catch{stopped=true;break}await delay(25)}assert.ok(stopped,'lease release must terminate the command');
  await writeFile(dir+'/config.yaml',config(true));
  await api('/commands',{id:'extra',title:'Extra',exec:[process.execPath,'--version']},'POST',403);
  for(const client of clients)await client.close().catch(()=>{});await stop();await boot();
  await api('/commands',{id:'extra',title:'Extra',exec:[process.execPath,'--version']},'POST',201);
  assert.equal(await readFile(dir+'/config.yaml','utf8'),config(true));
  await api('/commands/greet',actions[0],'PUT',403);
  const fresh=new BridgeClient({connection:first.connection,origin});clients.push(fresh);await fresh.connect();await grant(first.pairing.id,['extra']);
  await api('/commands/extra',{id:'extra',title:'Extra changed',exec:[process.execPath,'--version']},'PUT');
  await assert.rejects(fresh.submit({kind:'command',action:'extra',idempotencyKey:'stale-command-grant'}),error=>error.code==='command_not_granted');
  await grant(first.pairing.id,['extra']);const changed=await fresh.submit({kind:'command',action:'extra',idempotencyKey:'new-command-grant'});await api('/jobs/'+changed.id+'/decision',{approve:true});assert.equal((await collect(fresh.stream(changed.id))).exitCode,0);
  await fresh.close();await writeFile(dir+'/config.yaml',config(false));await stop();await boot();
  const state=await api('/state',undefined,'GET');assert.equal(state.allowWebCommandEditing,false);assert.ok(state.commands.some(c=>c.id==='extra'));
  await api('/commands/extra',undefined,'DELETE',403);
  console.log('Command E2E passed: YAML/grants/consent, literal arguments, signed progress, hierarchical routes, same-process sessions, live privacy omissions, normalized/redacted errors, idempotent lookup, cancellation and restart-only editing. No model requests.');
}finally{for(const client of clients)await client.close().catch(()=>{});await stop();await rm(dir,{recursive:true,force:true})}
