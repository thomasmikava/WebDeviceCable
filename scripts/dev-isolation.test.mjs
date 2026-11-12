// These checks never open a browser, OS URL handler or network socket.
import test from 'node:test';
import assert from 'node:assert/strict';
import {spawnSync} from 'node:child_process';
import {mkdir, mkdtemp, writeFile, readFile, rm, symlink} from 'node:fs/promises';
import {resolve} from 'node:path';
import {exampleEndpoint, assertExampleConnection} from '../examples/isolation.js';
import {testPort, testRuntime} from './test-instance.mjs';

test('example rejects installed/default/foreign endpoints before creating a client',()=>{
 for(const raw of [undefined,'http://127.0.0.1:47321','http://127.0.0.1:047321','http://localhost:47461','https://127.0.0.1:47461','http://127.0.0.1','http://127.0.0.1:47461/path','http://user@127.0.0.1:47461'])assert.throws(()=>exampleEndpoint(raw));
 const endpoint=exampleEndpoint('http://127.0.0.1:47461');assert.equal(endpoint,'http://127.0.0.1:47461');
 assert.throws(()=>assertExampleConnection({identity:{endpoint:'http://127.0.0.1:47321'}},endpoint));
 assertExampleConnection({identity:{endpoint}},endpoint);
});
test('example only supports invitation pairing and validated reconnection',async()=>{
 const source=await readFile(new URL('../examples/app.js',import.meta.url),'utf8');
 assert.doesNotMatch(source,/requestPairing|launchPairing|webdevicecable:\/\//);
 assert.match(source,/BridgeClient\.pair\(\{baseURL,/);
 assert.match(source,/new BridgeClient\(\{baseURL,/);
});
test('test overrides reject installed ports and directories outside disposable state',()=>{
 for(const value of ['47321','047321','-1','65536','NaN',''])assert.throws(()=>testPort(value));
 assert.equal(testPort('47461'),'47461');assert.equal(testPort('0'),'0');
 for(const dir of [resolve('.'),resolve('.local'),resolve('../outside'),resolve('.local/nested/unsafe')]){
  const child=spawnSync(process.execPath,['--input-type=module','-e',"import {testDirectory} from './scripts/test-instance.mjs';await testDirectory('guard');"],{encoding:'utf8',env:{...process.env,WDC_E2E_PORT:'47461',WDC_E2E_DATA_DIR:dir}});
  assert.notEqual(child.status,0,dir);
 }
});
test('test runtime refuses stale process IDs, installed ports and non-loopback URLs',async()=>{
 await mkdir('.local',{recursive:true});const dir=await mkdtemp(resolve('.local/isolation-check-'));
 try{
  for(const info of [{pid:8,url:'http://127.0.0.1:47461'},{pid:7,url:'http://127.0.0.1:47321'},{pid:7,url:'http://localhost:47461'},{pid:7,url:'https://evil.example:47461'}]){
   await writeFile(resolve(dir,'runner.json'),JSON.stringify(info));await assert.rejects(testRuntime(dir,{pid:7}));
  }
 }finally{await rm(dir,{recursive:true,force:true});}
});
test('test directory overrides cannot overwrite state or follow a symlink',async()=>{
 await mkdir('.local',{recursive:true});const dir=await mkdtemp(resolve('.local/isolation-check-')),link=dir+'-link';
 try{
  await writeFile(resolve(dir,'keep'),'fixture');
  if(process.platform!=='win32')await symlink(dir,link);
  for(const candidate of process.platform==='win32'?[dir]:[dir,link]){
   const child=spawnSync(process.execPath,['--input-type=module','-e',"import {testDirectory} from './scripts/test-instance.mjs';await testDirectory('guard');"],{encoding:'utf8',env:{...process.env,WDC_E2E_PORT:'47461',WDC_E2E_DATA_DIR:candidate}});assert.notEqual(child.status,0);
  }
  assert.equal(await readFile(resolve(dir,'keep'),'utf8'),'fixture');
 }finally{await rm(link,{force:true});await rm(dir,{recursive:true,force:true});}
});
