// Local artifact only: no registry publish, network request or lifecycle script.
import {mkdir, readFile, writeFile, copyFile, rm} from 'node:fs/promises';
import {resolve} from 'node:path';
import {spawnSync} from 'node:child_process';
const root = resolve(import.meta.dirname,'..'), out=resolve(root,'dist'), stage=resolve(out,'sdk');
const {version}=JSON.parse(await readFile(resolve(root,'package.json'),'utf8'));
await rm(stage,{recursive:true,force:true}); await mkdir(stage,{recursive:true});
for(const name of ['index.js','index.d.ts','secure.js','secure.d.ts','storage.js','storage.d.ts']) await copyFile(resolve(root,'internal/bridge/web',name),resolve(stage,name));
await copyFile(resolve(root,'LICENSE'),resolve(stage,'LICENSE'));
await copyFile(resolve(root,'docs/integrate.md'),resolve(stage,'INTEGRATE.md'));
await writeFile(resolve(stage,'README.md'),`# WebDeviceCable SDK\n\nZero runtime dependencies. Browser WebCrypto and IndexedDB; one client per tab.\nInstall this local archive, or copy all three JavaScript modules into your app.\n\nRead INTEGRATE.md for the copyable quickstart. Its advanced references are available in the full WDC archive's docs/ directory or [source repository](https://github.com/thomasmikava/WebDeviceCable).\nVersion ${version}; signed/encrypted protocol v2. No public registry package is implied.\n`);
await writeFile(resolve(stage,'package.json'),JSON.stringify({name:'@webdevicecable/sdk',version,type:'module',description:'Local WebDeviceCable browser SDK',license:'MIT',sideEffects:false,exports:{'.':{types:'./index.d.ts',import:'./index.js'}},files:['*.js','*.d.ts','LICENSE','README.md','INTEGRATE.md']},null,2)+'\n');
const result=spawnSync(process.platform==='win32'?'npm.cmd':'npm',['pack','--ignore-scripts','--offline','--cache',resolve(root,'.cache/npm'),'--pack-destination',out],{cwd:stage,stdio:'inherit',shell:process.platform==='win32'});
if(result.error) throw result.error;
if(result.status) process.exit(result.status);
