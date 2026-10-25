import assert from 'node:assert/strict';
import {mkdtemp,mkdir,rm,writeFile} from 'node:fs/promises';
import {resolve} from 'node:path';
import {spawnSync} from 'node:child_process';
await mkdir('.cache/tmp',{recursive:true}); const dir=await mkdtemp(resolve('.cache/tmp/sdk-consumer-'));
const npm=process.platform==='win32'?'npm.cmd':'npm';
function run(args,cwd=process.cwd()){const result=spawnSync(npm,args,{cwd,encoding:'utf8',shell:process.platform==='win32'});assert.equal(result.status,0,result.stdout+result.stderr);return result.stdout;}
try{
  run(['run','package:sdk']);
  await writeFile(resolve(dir,'package.json'),JSON.stringify({private:true,type:'module'}));
  run(['install',resolve('dist/webdevicecable-sdk-0.1.0.tgz'),'--ignore-scripts','--offline','--cache',resolve('.cache/npm'),'--no-audit'],dir);
  await writeFile(resolve(dir,'test.mjs'),"import assert from 'node:assert/strict';import {BridgeClient,ConnectionStore,BridgeSecurityError} from '@webdevicecable/sdk';assert.equal(typeof BridgeClient.requestPairing,'function');assert.equal(typeof ConnectionStore,'function');assert.equal(typeof BridgeSecurityError,'function');");
  const result=spawnSync(process.execPath,['test.mjs'],{cwd:dir,encoding:'utf8'});assert.equal(result.status,0,result.stderr);
  console.log('Packaged SDK installs and imports with zero runtime dependencies.');
}finally{await rm(dir,{recursive:true,force:true});}
