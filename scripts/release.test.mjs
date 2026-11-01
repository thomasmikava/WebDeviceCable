import assert from 'node:assert/strict';
import {generateKeyPairSync,createHash,sign} from 'node:crypto';
import {spawnSync} from 'node:child_process';
import {mkdtemp,writeFile,readFile,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join,resolve} from 'node:path';
import test from 'node:test';

test('community release verification authenticates the complete download set',async t=>{
 const dir=await mkdtemp(join(tmpdir(),'wdc-community-release-'));
 const {privateKey,publicKey}=generateKeyPairSync('ed25519');
 const trusted=join(dir,'trusted.pub');
 const pem=publicKey.export({format:'pem',type:'spki'});
 await writeFile(trusted,pem);
 const version='0.1.0',targets=['linux/amd64','linux/arm64','darwin/amd64','darwin/arm64','windows/amd64','windows/arm64'];
 const report={version,distribution:'community',automaticUpdates:false,releasePublicKey:publicKey.export({format:'der',type:'spki'}).subarray(-32).toString('base64'),sourceCommit:'a'.repeat(40),targets,artifacts:[]};
 for(const target of targets){const [os,arch]=target.split('/'),stem=`WebDeviceCable-${version}-${os}-${arch}`;report.artifacts.push(stem+(os==='linux'?'.tar.gz':'.zip'));if(os==='darwin')report.artifacts.push(stem+'.pkg');}
 const fixture=`import sys,json,io,tarfile,zipfile\nfrom pathlib import Path\np=Path(sys.argv[1]);r=json.loads(sys.argv[2])\nfor t in r['targets']:\n os,arch=t.split('/');stem=f"WebDeviceCable-{r['version']}-{os}-{arch}";b=json.dumps(dict(r,target=t,protocolVersion=2,stateVersion=1)).encode();name=stem+'/build-manifest.json'\n if os=='linux':\n  with tarfile.open(p/(stem+'.tar.gz'),'w:gz') as z:\n   i=tarfile.TarInfo(name);i.size=len(b);z.addfile(i,io.BytesIO(b))\n else:\n  with zipfile.ZipFile(p/(stem+'.zip'),'w') as z:z.writestr(name,b)\n if os=='darwin':(p/(stem+'.pkg')).write_bytes(b'fixture installer')`;
 const generated=spawnSync(process.platform==='win32'?'python':'python3',['-c',fixture,dir,JSON.stringify(report)],{encoding:'utf8'});
 assert.equal(generated.status,0,generated.stderr);
 await writeFile(join(dir,'build-manifest.json'),JSON.stringify(report));
 await writeFile(join(dir,'INSTALL.md'),'fixture installation guide');
 await writeFile(join(dir,'RELEASE_PUBLIC_KEY.pem'),pem);
 const files=[...report.artifacts,'build-manifest.json','INSTALL.md','RELEASE_PUBLIC_KEY.pem'].sort();
 async function checksum(){const lines=[];for(const file of files)lines.push(`${createHash('sha256').update(await readFile(join(dir,file))).digest('hex')}  ${file}\n`);return Buffer.from(lines.join(''));}
 async function signed(bytes){await writeFile(join(dir,'SHA256SUMS'),bytes);await writeFile(join(dir,'SHA256SUMS.sig'),sign(null,Buffer.concat([Buffer.from('WebDeviceCable community checksums v1\0'),bytes]),privateKey));}
 function verify(key=trusted){return spawnSync(process.execPath,[resolve('scripts/release.mjs'),'verify-community','--dir',dir,'--public-key',key],{encoding:'utf8'});}
 try{
  const original=await checksum();await signed(original);
  await t.test('all six archives and both installers pass under the pinned key',()=>{const result=verify();assert.equal(result.status,0,result.stderr);assert.match(result.stdout,/all six targets/);});
  await t.test('changed bytes fail even when the file remains a valid archive',async()=>{const path=join(dir,'INSTALL.md');await writeFile(path,'changed instructions');assert.match(verify().stderr,/checksum mismatch/);await writeFile(path,'fixture installation guide');});
  await t.test('a replacement key or unsigned checksum change is rejected',async()=>{const other=join(dir,'other.pub');await writeFile(other,generateKeyPairSync('ed25519').publicKey.export({format:'pem',type:'spki'}));assert.match(verify(other).stderr,/signature verification failed/);await writeFile(join(dir,'SHA256SUMS'),Buffer.concat([original,Buffer.from('\n')]));assert.match(verify().stderr,/signature verification failed/);await signed(original);});
  await t.test('a signed but incomplete or traversing file list is rejected',async()=>{await signed(Buffer.from(original.toString().split('\n').slice(1).join('\n')));assert.match(verify().stderr,/incomplete/);await signed(Buffer.from('a'.repeat(64)+'  ../outside\n'));assert.match(verify().stderr,/Invalid or duplicate/);await signed(original);});
  await t.test('a signed release cannot enable automatic updates in its manifest',async()=>{await writeFile(join(dir,'build-manifest.json'),JSON.stringify({...report,automaticUpdates:true}));await signed(await checksum());assert.match(verify().stderr,/Invalid community release manifest/);});
 }finally{await rm(dir,{recursive:true,force:true});}
});
