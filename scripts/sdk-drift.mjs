import {readFile} from 'node:fs/promises';
import {spawnSync} from 'node:child_process';
const files=['index.js','index.d.ts','secure.js','secure.d.ts','storage.js','storage.d.ts','relay.js','relay.d.ts'].map(file=>new URL('../internal/bridge/web/'+file,import.meta.url));
const before=await Promise.all(files.map(file=>readFile(file)));
const build=spawnSync(process.platform==='win32'?'npm.cmd':'npm',['run','build:sdk'],{stdio:'inherit',shell:process.platform==='win32'});
if(build.status) process.exit(build.status);
for(let i=0;i<files.length;i++) if(!before[i].equals(await readFile(files[i]))) throw new Error(`Generated SDK drift: ${files[i].pathname}. Run npm run build:sdk and include the outputs.`);
