import { existsSync } from 'node:fs';
// Real runner + SDK launch rendezvous. Does not register OS handlers or call providers.
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {once} from 'node:events';
import {mkdtemp,mkdir,readFile,writeFile,rm} from 'node:fs/promises';
import {resolve} from 'node:path';
import {BridgeClient} from '../internal/bridge/web/index.js';
import {createSigningKeys,launchRequest,parseInvitation,SecureTransport} from '../internal/bridge/web/secure.js';
const delay=ms=>new Promise(r=>setTimeout(r,ms)),root=resolve('.cache/tmp');await mkdir(root,{recursive:true});const dir=await mkdtemp(root+'/launch-e2e-');let child,client,base,auth;
try{
 await writeFile(dir+'/config.yaml','bridge:\n  disableNative: true\nactions: []\n',{mode:0o600});
 child=spawn(resolve(process.env.WDC_BINARY ?? (existsSync(process.platform==='win32'?'dist/WebDeviceCable.exe':'dist/WebDeviceCable') ? (process.platform==='win32'?'dist/WebDeviceCable.exe':'dist/WebDeviceCable') : (process.platform==='win32'?'dist/webdevicecable.exe':'dist/webdevicecable'))),['serve','--data-dir',dir,'--port','0'],{stdio:'pipe'});
 for(let i=0;i<150;i++){try{base=JSON.parse(await readFile(dir+'/runner.json','utf8')).url;break}catch{await delay(50)}}assert.ok(base);
 const controlKey=await readFile(dir+'/admin.key','utf8');auth=await fetch(base+'/api/session',{method:'POST',headers:{Origin:base,Authorization:'Bearer '+controlKey}}).then(r=>r.json());
 async function api(path,body){const r=await fetch(base+'/api'+path,{method:'POST',headers:{Origin:base,'Content-Type':'application/json','X-Bridge-Session':auth.session,'X-Bridge-CSRF':auth.csrf},body:JSON.stringify(body)});assert.ok(r.ok,await r.clone().text());return r.json();}
 const keys=await createSigningKeys(),origin='https://launch.example:8443',details={requestId:crypto.randomUUID().replaceAll('-','')+crypto.randomUUID().replaceAll('-',''),appId:'launch.example',appUser:'alice',name:'Launch check',logoURL:'https://launch.example:8443/logo.png',requestedPermissions:{automaticApprovals:true,files:true}};
 await assert.rejects(launchRequest(base,origin,keys,details),e=>e.code==='launch_required');
 const uri='webdevicecable://connect?'+new URLSearchParams({requestId:details.requestId,origin,publicKey:keys.publicKey});assert.ok(!uri.includes('alice')&&!uri.includes(controlKey));
 const intent=await fetch(base+'/api/launch-intents',{method:'POST',headers:{Authorization:'Bearer '+controlKey,'Content-Type':'application/json'},body:JSON.stringify({uri})});assert.equal(intent.status,201);
 let request=await launchRequest(base,origin,keys,details);assert.equal(request.status,'pending');assert.equal(request.invitation,'');
 await api(`/pairing-requests/${details.requestId}/decision`,{approve:true,automatic:false,files:false,logoId:''});
 request=await launchRequest(base,origin,keys,details,request.identity);assert.equal(request.status,'approved');const invite=parseInvitation(request.invitation);
 await assert.rejects(BridgeClient.pair({baseURL:base,origin,invitation:request.invitation,appId:details.appId,appUser:details.appUser}),e=>e.code==='identity_mismatch');
 const transport=new SecureTransport(request.identity,origin,keys,'');let result;
 try{const response=await transport.fetch('/v2/pairings/claim',{method:'POST',body:JSON.stringify({appId:details.appId,appUser:details.appUser})},'',invite.secret);assert.equal(response.status,201);result=await response.json();assert.equal(result.pairing.automatic,false);assert.equal(result.pairing.files,false);}finally{await transport.close();}
 client=new BridgeClient({origin,connection:{...keys,identity:request.identity,pairingId:result.pairing.id}});await client.connect();assert.equal((await client.capabilities()).pairing.origin,origin);
 const job=await client.submit({kind:'connection-test',idempotencyKey:crypto.randomUUID()});assert.equal(job.status,'awaiting_approval');await api(`/jobs/${job.id}/decision`,{approve:true});let final;for await(const update of client.stream(job.id))final=update;assert.equal(final.status,'completed');
 console.log('Launch E2E passed: installed intent, HTTPS origin, signatures, encrypted invitation, mandatory owner consent, requesting-key binding and signed streaming. No OS registration or model calls.');
}finally{await client?.close().catch(()=>{});if(child&&child.exitCode===null){const stopped=once(child,'exit');child.kill('SIGTERM');await stopped};await rm(dir,{recursive:true,force:true});}
