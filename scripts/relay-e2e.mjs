// Disposable Docker relay + one isolated desktop, with simultaneous direct and relay clients.
import assert from 'node:assert/strict';
import {spawn,spawnSync} from 'node:child_process';
import {once} from 'node:events';
import {mkdtemp,mkdir,readFile,writeFile,rm} from 'node:fs/promises';
import {resolve} from 'node:path';
import {createServer} from 'node:net';
import {pathToFileURL} from 'node:url';
import {RelayClient as CurrentRelayClient,RelayAdminClient as CurrentRelayAdminClient} from '../internal/bridge/web/relay.js';
import {BridgeClient as CurrentBridgeClient} from '../internal/bridge/web/index.js';
const {BridgeClient}=process.env.RORIDO_WDC_SDK ? await import(pathToFileURL(resolve(process.env.RORIDO_WDC_SDK)).href) : {BridgeClient:CurrentBridgeClient};
const {RelayClient,RelayAdminClient}=process.env.WDC_RELAY_SDK ? await import(pathToFileURL(resolve(process.env.WDC_RELAY_SDK)).href) : {RelayClient:CurrentRelayClient,RelayAdminClient:CurrentRelayAdminClient};
const native=process.argv.includes('--native');
const root=resolve('.cache/tmp');await mkdir(root,{recursive:true});const dir=await mkdtemp(root+'/relay-e2e-');
const name='wdc-test-'+crypto.randomUUID(),volume=name+'-data',secret=Buffer.from(crypto.getRandomValues(new Uint8Array(32))).toString('hex');
let key,admin,project,second,secondProject,secondEnrollment;
const origin='https://kings.example',directOrigin='http://127.0.0.1:47322';let child,base,control,backend,direct;
function docker(...args){const p=spawnSync('docker',args,{encoding:'utf8'});assert.equal(p.status,0,p.stderr);return p.stdout.trim()}
const wait=ms=>new Promise(resolve=>setTimeout(resolve,ms));
async function api(path,body,method='POST'){
 const r=await fetch(base+'/api'+path,{method,headers:{Origin:base,'Content-Type':'application/json','X-Bridge-Session':control.session,'X-Bridge-CSRF':control.csrf},...(body===undefined?{}:{body:JSON.stringify(body)})});const data=await r.json();assert.ok(r.ok,JSON.stringify(data));return data;
}
try {
 // mkdtemp creates a private host directory; the read-only bind must also be
 // readable by the container's non-root UID on Linux.
 await writeFile(dir+'/relay.key',secret,{mode:0o444});
 const portProbe=createServer();await new Promise(resolve=>portProbe.listen(0,'127.0.0.1',resolve));const port=portProbe.address().port;await new Promise(resolve=>portProbe.close(resolve));
 docker('run','-d','--rm','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--pids-limit','128','--memory','1g','--cpus','1','--name',name,'-p','127.0.0.1:'+port+':8080','--mount','type=bind,source='+dir+'/relay.key,target=/run/secrets/relay_key,readonly','--mount','type=volume,source='+volume+',target=/data',
   '-e','WDC_ADMIN_KEY_FILE=/run/secrets/relay_key','wdc-relay:local');
 const url='http://'+docker('port',name,'8080/tcp');
 for(let i=0;i<100;i++){try{if((await fetch(url+'/readyz')).ok)break;}catch{}await wait(50)}
 assert.equal(docker('exec',name,'id','-u'),'10001');
 admin=new RelayAdminClient({baseURL:url,adminKey:secret});
 project=await admin.createProject({appId:'kings-international',name:'Kings',origin});key=project.apiKey;
 secondProject=await admin.createProject({appId:'other-website',name:'Other website',origin:'https://other.example'});
 assert.equal((await admin.listProjects()).length,2);
 assert.equal((await admin.getProject({projectId:project.project.id})).appId,'kings-international');
 await assert.rejects(new RelayAdminClient({baseURL:url,adminKey:key}).listProjects(),e=>e.status===401);
 await mkdir(dir+'/desktop');await writeFile(dir+'/desktop/config.yaml','bridge:\n  disableNative: '+!native+'\nactions: []\n');
 child=spawn(resolve(process.env.WDC_BINARY??'.cache/bin/webdevicecable'),['serve','--data-dir',dir+'/desktop','--port','0'],{stdio:'pipe'});
 let stderr='';child.stderr.on('data',data=>stderr+=data);
 for(let i=0;i<150;i++){try{base=JSON.parse(await readFile(dir+'/desktop/runner.json','utf8')).url;break}catch{}await wait(50)}assert.ok(base,stderr);
 control=await fetch(base+'/api/session',{method:'POST',headers:{Origin:base,Authorization:'Bearer '+await readFile(dir+'/desktop/admin.key','utf8')}}).then(r=>r.json());
 if(native){for(let i=0;i<250;i++){const state=await api('/state',undefined,'GET');if(!state.nativeChecking&&state.native.length)break;await wait(100)}await api('/sharing',{names:true,models:true,versions:true},'PATCH')}
 const enrollment=await RelayClient.enroll({baseURL:url,apiKey:key,appUser:'admin-7'});
 const expected=await api('/relay/inspect',{url,code:enrollment.code});assert.equal(expected.appUser,'admin-7');
 await api('/relay/connect',{url,code:enrollment.code,expected});
 backend=new RelayClient({baseURL:url,apiKey:key,connectionId:enrollment.connectionId,account:{appId:'kings-international',appUser:'admin-7'},origin,groups:[{id:'ai',name:'Kings AI',kind:'ai',children:[{id:'generation',name:'Generation',kind:'ai'},{id:'images',name:'Images',kind:'ai'}]}]});
 for(let i=0;i<100;i++){if((await backend.status()).connected)break;await wait(50)}
 await backend.connect();const caps=await backend.capabilities();assert.equal(caps.leaseSeconds,45);assert.equal(caps.pairing.appUser,'admin-7');
 secondEnrollment=await RelayClient.enroll({baseURL:url,apiKey:secondProject.apiKey,appUser:'admin-7'});
 const secondExpected=await api('/relay/inspect',{url,code:secondEnrollment.code});assert.equal(secondExpected.appId,'other-website');
 await api('/relay/connect',{url,code:secondEnrollment.code,expected:secondExpected});
 second=new RelayClient({baseURL:url,apiKey:secondProject.apiKey,connectionId:secondEnrollment.connectionId,account:{appId:'other-website',appUser:'admin-7'},origin:secondExpected.origin});
 for(let i=0;i<100;i++){if((await second.status()).connected)break;await wait(50)}
 await second.connect();const secondCaps=await second.capabilities();assert.equal(secondCaps.pairing.appId,'other-website');
 await api('/pairings/'+secondCaps.pairing.id,{automatic:true},'PATCH');
 await assert.rejects(RelayClient.revoke({baseURL:url,apiKey:secondProject.apiKey,connectionId:enrollment.connectionId,appUser:'admin-7'}),e=>e.status===404);
 const crossed=new RelayClient({baseURL:url,apiKey:secondProject.apiKey,connectionId:enrollment.connectionId,account:{appId:'kings-international',appUser:'admin-7'},origin});
 await assert.rejects(crossed.capabilities(),e=>e.status===403);
 const invitation=await api('/invitations',{appId:'rorido',name:'Rorido',origin:directOrigin,appUser:'fixture-user',mode:'restricted',automatic:true});
 const paired=await BridgeClient.pair({baseURL:base,origin:directOrigin,invitation:invitation.invitation,appId:'rorido',appUser:'fixture-user'});
 direct=new BridgeClient({connection:paired.connection,origin:directOrigin});await direct.connect();assert.equal((await direct.capabilities()).leaseSeconds,12);
 await api('/pairings/'+caps.pairing.id,{automatic:true},'PATCH');
 const [kings,rorido,otherJob]=await Promise.all([backend.run({kind:'connection-test',idempotencyKey:'docker-kings-1'}),direct.run({kind:'connection-test',idempotencyKey:'docker-rorido-1'}),second.run({kind:'connection-test',idempotencyKey:'docker-kings-1'})]);
 assert.equal(kings.status,'completed');assert.equal(rorido.status,'completed');assert.equal(kings.billing,'none');
 await assert.rejects(direct.get(kings.id),e=>e.status===404);
 assert.equal(otherJob.status,'completed');assert.notEqual(otherJob.id,kings.id);await assert.rejects(second.get(kings.id),e=>e.status===404);
 const file=await backend.upload(new Blob(['fixture task'],{type:'text/plain'}),'task.txt');assert.equal(file.size,12);await backend.removeInput(file.id);
 if(native){
   await api('/pairings/'+caps.pairing.id+'/routing',{expectedRevision:0,routing:{default:{provider:'codex',reasoning:'low'}}},'PUT');
   assert.equal((await backend.preflight({kind:'action',group:'ai.generation',prompt:'fixture'},['text'])).available,true);
   const [local,web]=await Promise.all([backend.run({group:'ai.generation',prompt:'Reply with exactly: relay-fixture-ok',timeoutSeconds:180}),direct.run({kind:'text',provider:'claude',prompt:'Reply with exactly: direct-fixture-ok',timeoutSeconds:180})]);
   assert.match(local.output,/relay-fixture-ok/);assert.match(web.output,/direct-fixture-ok/);
   const schema={type:'object',properties:{ok:{type:'boolean'}},required:['ok'],additionalProperties:false};
   const structured=await backend.run({group:'ai.generation',prompt:'Return JSON with ok equal to true.',outputSchema:schema,timeoutSeconds:180});assert.deepEqual(structured.json,{ok:true});
   const session=backend.session({group:'ai.generation'});await session.run('Remember this fixture word: saffron. Reply OK.');assert.match((await session.run('What fixture word did I give you? Reply with the word.')).output,/saffron/i);await session.close();
   let calls=0;await backend.registerTools([{id:'echo',title:'Echo',description:'Return a fixture verification value.',readOnly:true,inputSchema:{type:'object',properties:{value:{type:'string'}},required:['value'],additionalProperties:false},execute:({value})=>{calls++;return {value,marker:'relay-tool-confirmed'}}}]);
   const tool=await backend.run({group:'ai.generation',mode:'app-tools',prompt:'Call the app echo tool once with value saffron. Report the returned marker.',timeoutSeconds:180});assert.equal(calls,1);assert.match(tool.output,/relay-tool-confirmed/);
   const image=await backend.run({kind:'image-generate',provider:'codex',group:'ai.images',prompt:'Generate a small simple image using the built-in image generation tool: a yellow circle on a white background.',timeoutSeconds:240});const blob=await backend.artifact(image);assert.equal(blob.type,'image/png');
   const input=await backend.upload(blob,'reference.png');const edited=await backend.run({kind:'image-edit',provider:'codex',group:'ai.images',inputs:[input.id],prompt:'Use the image generation tool to change the circle to blue and preserve the white background.',timeoutSeconds:240});assert.notEqual(edited.artifacts[0].sha256,image.artifacts[0].sha256);assert.ok((await backend.artifact(edited)).size>1000);
   console.log('Native relay parity passed: simultaneous Codex/Claude, feature-group admission, JSON, continuing session, once-only app tool, image generation and reference-image editing.');
 }
 // Restart only the disposable relay. Completed jobs recover by their stable inference key.
 const rotated=await admin.rotateProjectKey({projectId:project.project.id});
 await assert.rejects(backend.status(),e=>e.status===401);
 backend.relay.apiKey=key=rotated.apiKey;
 assert.equal((await backend.status()).connected,true);
 await backend.close();docker('restart',name);
 for(let i=0;i<150;i++){try{if((await backend.status()).connected)break;}catch{}await wait(100)}
 await backend.connect();assert.equal((await backend.lookup('docker-kings-1')).id,kings.id);
 assert.equal((await second.lookup('docker-kings-1')).id,otherJob.id);assert.equal((await admin.listProjects()).length,2);
 assert.equal((await direct.run({kind:'connection-test',idempotencyKey:'direct-after-relay-restart'})).status,'completed');
 const pending=await backend.submit({kind:'connection-test',idempotencyKey:'revocation-test'});
 await admin.revokeProject({projectId:project.project.id});
 await assert.rejects(backend.capabilities());
 for(let i=0;i<100;i++){const state=await api('/state',undefined,'GET');if(state.jobs.find(j=>j.id===pending.id)?.status==='cancelled')break;await wait(50)}
 assert.equal((await api('/state',undefined,'GET')).jobs.find(j=>j.id===pending.id).status,'cancelled');
 assert.equal((await second.run({kind:'connection-test',idempotencyKey:'other-after-project-revocation'})).status,'completed');
 assert.equal((await admin.getProject({projectId:project.project.id})).revoked,true);
 console.log('Dynamic projects passed: operator-only registration, separate credentials and desktop consent, identical account/operation IDs isolated, key rotation, durable restart, project revocation cancels work and leaves the other website connected.');
 console.log('Docker relay + Rorido direct passed concurrently: non-root, pairing, crypto, 45s/12s leases, streamed jobs, files, isolation, relay restart recovery and revocation. Disposable state only.');
} catch(error) { const result=spawnSync('docker',['logs',name],{encoding:'utf8'});console.error(result.stdout,result.stderr);throw error; } finally {
 await backend?.close().catch(()=>{});await second?.close().catch(()=>{});await direct?.close().catch(()=>{});
 if(child&&child.exitCode===null){const exit=once(child,'exit');child.kill('SIGTERM');await exit}
 spawnSync('docker',['rm','-f',name],{stdio:'ignore'});spawnSync('docker',['volume','rm',volume],{stdio:'ignore'});
 await rm(dir,{recursive:true,force:true});
}
