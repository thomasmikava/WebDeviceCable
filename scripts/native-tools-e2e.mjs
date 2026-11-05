import { existsSync } from 'node:fs';
// Opt-in native subscription test; no API fallback. Run from the repository root.
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {once} from 'node:events';
import {mkdtemp,mkdir,readFile,writeFile,rm} from 'node:fs/promises';
import {resolve} from 'node:path';
import {BridgeClient} from '../internal/bridge/web/index.js';
const wait=ms=>new Promise(r=>setTimeout(r,ms)),root=resolve('.cache/tmp');await mkdir(root,{recursive:true});
const dir=await mkdtemp(root+'/native-tools-'),workspace=await mkdtemp(root+'/granted-folder-');await writeFile(workspace+'/note.txt','Original cable note.');
const provider=process.argv[2]??'codex';assert.ok(['codex','claude'].includes(provider),'Use codex or claude');
const origin='https://wdc-native.example:8443';let child,client,base,auth;
try {
 child=spawn(resolve(process.env.WDC_BINARY ?? (existsSync(process.platform==='win32'?'dist/WebDeviceCable.exe':'dist/WebDeviceCable') ? (process.platform==='win32'?'dist/WebDeviceCable.exe':'dist/WebDeviceCable') : (process.platform==='win32'?'dist/webdevicecable.exe':'dist/webdevicecable'))),['serve','--data-dir',dir,'--port','0'],{stdio:'pipe'});
 for(let i=0;i<150;i++){try{base=JSON.parse(await readFile(dir+'/runner.json','utf8')).url;break}catch{await wait(100)}}assert.ok(base);
 auth=await fetch(base+'/api/session',{method:'POST',headers:{Origin:base,Authorization:'Bearer '+await readFile(dir+'/admin.key','utf8')}}).then(r=>r.json());
 async function api(path,body,method='POST'){const r=await fetch(base+'/api'+path,{method,headers:{Origin:base,'Content-Type':'application/json','X-Bridge-Session':auth.session,'X-Bridge-CSRF':auth.csrf},...(body===undefined?{}:{body:JSON.stringify(body)})});const value=await r.json();assert.ok(r.ok,JSON.stringify(value));return value;}
 for(let i=0;i<250;i++){const state=await api('/state',undefined,'GET');if(!state.nativeChecking&&state.native.length){assert.ok(state.native.find(n=>n.id===provider)?.execution,JSON.stringify(state.native));break;}await wait(100);}
 await api("/sharing",{names:true,versions:true,models:true,rawErrors:false},"PATCH");
 const invitation=await api('/invitations',{appId:'native-tools',name:'Native tools QA',appUser:'owner',origin,automatic:true});
 const paired=await BridgeClient.pair({origin,baseURL:base,invitation:invitation.invitation,appId:'native-tools',appUser:'owner'});let callbacks=0;
 const tools=[{id:'echo',title:'Echo value',description:'Return the exact input value with a verification marker.',readOnly:true,inputSchema:{type:'object',properties:{value:{type:'string'}},required:['value'],additionalProperties:false},execute:({value})=>{callbacks++;return {value,marker:'tool-confirmed'};}}];client=new BridgeClient({origin,connection:paired.connection});await client.connect();
 await api(`/pairings/${paired.pairing.id}/permissions`,{folders:[{path:workspace,write:true,automatic:false}]},'PUT');
 const session=client.session({provider,mode:'workspace',tools});const job=await session.send('Use the app echo tool with value "saffron". Then use the WDC folder tool to read note.txt in the granted folder. Reply with the returned marker, the value and the file contents. You must actually call both tools.');
 let final;for await(const update of client.stream(job.id))final=update;
 assert.ok(final,'No terminal job returned');assert.equal(final.status,'completed');assert.equal(callbacks,1);assert.match(final.output,/tool-confirmed/);assert.match(final.output,/Original cable note/);
 const edit=await session.send('Use the WDC write tool to replace note.txt with exactly "Edited by the local bridge.". Read the current file first if needed. Then reply "done".');
 const watch=(async()=>{for(let i=0;i<300;i++){const state=await api('/state',undefined,'GET');const pending=state.permissions.find(p=>p.kind==='file-write');if(pending){assert.match(pending.before,/Original cable note/);await api(`/permissions/${pending.id}/decision`,{approve:true});return;}await wait(150);}throw Error('No write approval request');})();
 for await(const update of client.stream(edit.id))final=update;await watch;assert.equal(final.status,'completed');assert.equal(await readFile(workspace+'/note.txt','utf8'),'Edited by the local bridge.');console.log(provider,'native app tools + folder read + approved edit passed');
 if(process.argv.includes('--idle')){await wait(65000);const last=await session.run('What exact value did the echo tool return earlier? Reply only with the value.');assert.match(last.output,/saffron/);console.log(provider,'context retained after 65 seconds idle');}
 await session.close();
}finally{await client?.close().catch(()=>{});if(child&&child.exitCode===null){const stopped=once(child,'exit');child.kill('SIGTERM');await stopped;}await rm(dir,{recursive:true,force:true});await rm(workspace,{recursive:true,force:true});}
