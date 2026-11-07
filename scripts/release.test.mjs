import assert from 'node:assert/strict';
import {generateKeyPairSync,createHash,sign} from 'node:crypto';
import {spawnSync} from 'node:child_process';
import {readFileSync} from 'node:fs';
import {mkdtemp,writeFile,readFile,rm,mkdir} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join,resolve} from 'node:path';
import test from 'node:test';

const downloadNames=JSON.parse(readFileSync(resolve('scripts/download-names.json'),'utf8'));
async function fixture(t,legacy=false){
 const dir=await mkdtemp(join(tmpdir(),'wdc community release '));
 t.after(()=>rm(dir,{recursive:true,force:true}));
 const {privateKey,publicKey}=generateKeyPairSync('ed25519');
 const trusted=join(dir,'trusted.pub');
 const pem=publicKey.export({format:'pem',type:'spki'});
 await writeFile(trusted,pem);
 const version='0.1.0',targets=['linux/amd64','linux/arm64','darwin/amd64','darwin/arm64','windows/amd64','windows/arm64'];
 const report={version,distribution:'community',automaticUpdates:false,releasePublicKey:publicKey.export({format:'der',type:'spki'}).subarray(-32).toString('base64'),sourceCommit:'a'.repeat(40),targets,artifacts:[]};
 for(const target of targets){const [os,arch]=target.split('/'),stem=legacy?`WebDeviceCable-${version}-${os}-${arch}`:`WebDeviceCable-${version}-${downloadNames[target]}`;report.artifacts.push(stem+(!legacy&&os==='windows'?'-Setup.exe':os==='linux'?'.tar.gz':'.zip'));if(os==='darwin')report.artifacts.push(stem+'.pkg');}
 const archives=`import sys,json,io,tarfile,zipfile\nfrom pathlib import Path\np=Path(sys.argv[1]);r=json.loads(sys.argv[2]);names=json.loads(sys.argv[3]);legacy=sys.argv[4]=='true'\nfor t in r['targets']:\n os,arch=t.split('/');stem=f"WebDeviceCable-{r['version']}-{os}-{arch}";download=stem if legacy else f"WebDeviceCable-{r['version']}-{names[t]}";b=json.dumps(dict(r,target=t,protocolVersion=2,stateVersion=1)).encode();name=stem+'/build-manifest.json'\n if os=='windows' and not legacy:continue\n if os=='linux':\n  with tarfile.open(p/(download+'.tar.gz'),'w:gz') as z:\n   i=tarfile.TarInfo(name);i.size=len(b);z.addfile(i,io.BytesIO(b))\n else:\n  with zipfile.ZipFile(p/(download+'.zip'),'w') as z:z.writestr(name,b)\n if os=='darwin':(p/(download+'.pkg')).write_bytes(b'fixture installer')`;
 const generated=spawnSync(process.platform==='win32'?'python':'python3',['-c',archives,dir,JSON.stringify(report),JSON.stringify(downloadNames),String(legacy)],{encoding:'utf8'});
 assert.equal(generated.status,0,generated.stderr);
 async function installer(arch,manifest){
  const stage=join(dir,`stage-${arch}`);await mkdir(stage,{recursive:true});
  for(const file of ['WebDeviceCable.exe','WebDeviceCable Launcher.exe','Install.ps1','Uninstall.cmd','Uninstall.ps1','LICENSE','THIRD_PARTY_NOTICES.md'])await writeFile(join(stage,file),'fixture payload');
  await writeFile(join(stage,'build-manifest.json'),JSON.stringify(manifest));
  const output=join(dir,`WebDeviceCable-${version}-${downloadNames[`windows/${arch}`]}-Setup.exe`);
  const prefix=process.platform==='win32'?'/':'-';
  const definitions={VERSION:version,NUMERIC_VERSION:version,ARCH:arch,ARCH_LABEL:arch==='amd64'?'x64':'ARM64',STAGE:stage,OUTPUT:output};
  const result=spawnSync(process.env.WDC_MAKENSIS??'makensis',[prefix+'V2',...Object.entries(definitions).map(([key,value])=>`${prefix}D${key}=${value}`),resolve('scripts/windows-installer.nsi')],{encoding:'utf8'});
  assert.equal(result.status,0,result.stderr??result.error?.message);
 }
 if(!legacy)for(const arch of ['amd64','arm64'])await installer(arch,{...report,target:`windows/${arch}`,protocolVersion:2,stateVersion:1});
 await writeFile(join(dir,'build-manifest.json'),JSON.stringify(report));
 await writeFile(join(dir,'INSTALL.md'),'fixture installation guide');
 await writeFile(join(dir,'RELEASE_PUBLIC_KEY.pem'),pem);
 const files=[...report.artifacts,'build-manifest.json','INSTALL.md','RELEASE_PUBLIC_KEY.pem'].sort();
 async function checksum(){const lines=[];for(const file of files)lines.push(`${createHash('sha256').update(await readFile(join(dir,file))).digest('hex')}  ${file}\n`);return Buffer.from(lines.join(''));}
 async function signed(bytes){await writeFile(join(dir,'SHA256SUMS'),bytes);await writeFile(join(dir,'SHA256SUMS.sig'),sign(null,Buffer.concat([Buffer.from('WebDeviceCable community checksums v1\0'),bytes]),privateKey));}
 function verify(key=trusted){return spawnSync(process.execPath,[resolve('scripts/release.mjs'),'verify-community','--dir',dir,'--public-key',key],{encoding:'utf8'});}
 return {dir,report,checksum,signed,verify,installer};
}

test('community release verification authenticates the complete download set',async t=>{
 const {dir,report,checksum,signed,verify,installer}=await fixture(t);
  const original=await checksum();await signed(original);
  await t.test('all six targets including real Windows EXEs pass under the pinned key',()=>{const result=verify();assert.equal(result.status,0,result.stderr);assert.match(result.stdout,/all six targets/);});
  await t.test('changed bytes fail even when the file remains a valid archive',async()=>{const path=join(dir,'INSTALL.md');await writeFile(path,'changed instructions');assert.match(verify().stderr,/checksum mismatch/);await writeFile(path,'fixture installation guide');});
  await t.test('a replacement key or unsigned checksum change is rejected',async()=>{const other=join(dir,'other.pub');await writeFile(other,generateKeyPairSync('ed25519').publicKey.export({format:'pem',type:'spki'}));assert.match(verify(other).stderr,/signature verification failed/);await writeFile(join(dir,'SHA256SUMS'),Buffer.concat([original,Buffer.from('\n')]));assert.match(verify().stderr,/signature verification failed/);await signed(original);});
  await t.test('a signed but incomplete or traversing file list is rejected',async()=>{await signed(Buffer.from(original.toString().split('\n').slice(1).join('\n')));assert.match(verify().stderr,/incomplete/);await signed(Buffer.from('a'.repeat(64)+'  ../outside\n'));assert.match(verify().stderr,/Invalid or duplicate/);await signed(original);});
  await t.test('a signed release cannot enable automatic updates in its manifest',async()=>{await writeFile(join(dir,'build-manifest.json'),JSON.stringify({...report,automaticUpdates:true}));await signed(await checksum());assert.match(verify().stderr,/Invalid community release manifest/);});
  await t.test('a signed Windows installer from a different source commit is rejected',async()=>{
   await writeFile(join(dir,'build-manifest.json'),JSON.stringify(report));
   await installer('amd64',{...report,target:'windows/amd64',protocolVersion:2,stateVersion:1,sourceCommit:'b'.repeat(40)});
   await signed(await checksum());assert.match(verify().stderr,/Inconsistent artifact manifest.*sourceCommit/);
  });
  await t.test('a signed ARM64 installer cannot masquerade as an x64 download',async()=>{
   await installer('amd64',{...report,target:'windows/arm64',protocolVersion:2,stateVersion:1});
   await signed(await checksum());assert.match(verify().stderr,/Incompatible artifact/);
  });
});

test('previously published downloads still verify under their original names',async t=>{
 const {checksum,signed,verify}=await fixture(t,true);
 await signed(await checksum());const result=verify();assert.equal(result.status,0,result.stderr);
});
