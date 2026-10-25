// Real detached launcher lifecycle, private state stays inside this repository.
import assert from 'node:assert/strict';
import {mkdir,mkdtemp,readFile,rm} from 'node:fs/promises';
import {resolve} from 'node:path';
import {spawnSync} from 'node:child_process';
await mkdir('.cache/tmp',{recursive:true}); const dir=await mkdtemp(resolve('.cache/tmp/cli-e2e-'));
const binary=resolve(process.platform==='win32'?'dist/webdevicecable.exe':'dist/webdevicecable');
function cli(command,...args){const result=spawnSync(binary,[command,'--data-dir',dir,...args],{encoding:'utf8',timeout:15000});assert.equal(result.status,0,result.stdout+result.stderr);return result.stdout.trim();}
try{
  cli('start','--no-open','--port','0');
  const first=JSON.parse(await readFile(resolve(dir,'runner.json'),'utf8'));
  cli('start','--no-open','--port','0');
  assert.equal(JSON.parse(await readFile(resolve(dir,'runner.json'),'utf8')).pid,first.pid);
  const link=cli('open','--print'); assert.ok(link.startsWith(first.url+'/#key='));
  cli('stop');
  for(let i=0;i<60;i++){try{await readFile(resolve(dir,'runner.json'));await new Promise(resolve=>setTimeout(resolve,50));}catch{break;}}
  const closed=spawnSync(binary,['open','--data-dir',dir,'--print'],{encoding:'utf8'});assert.notEqual(closed.status,0);
  console.log('CLI passed: authenticated start, existing-runner reuse, private open and graceful stop.');
}finally{spawnSync(binary,['stop','--data-dir',dir],{stdio:'ignore',timeout:5000});await rm(dir,{recursive:true,force:true});}
