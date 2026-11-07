// Local maintainer tool. Does not upload, publish, install, or change Git settings.
import {generateKeyPairSync,createPrivateKey,createPublicKey,sign,verify,createHash} from 'node:crypto';
import {readFile,writeFile,mkdir,copyFile,mkdtemp,rm,readdir,lstat} from 'node:fs/promises';
import {resolve,relative,sep,join} from 'node:path';
import {spawnSync} from 'node:child_process';
import {readFileSync} from 'node:fs';
const root=resolve(import.meta.dirname,'..');
const downloadNames=JSON.parse(readFileSync(join(root,'scripts/download-names.json'),'utf8'));
function downloadStem(version,os,arch){return `WebDeviceCable-${version}-${downloadNames[`${os}/${arch}`]}`;}
function inspectArtifact(file,stem){return JSON.parse(run(process.platform==='win32'?'python':'python3',[join(root,'scripts/inspect-artifact.py'),file,`${stem}/build-manifest.json`]));}
const params=process.argv.slice(2),command=params.shift();
function option(name,fallback){const i=params.indexOf(name);if(i<0)return fallback;if(!params[i+1]||params[i+1].startsWith('--'))throw Error(`${name} needs a value`);return params[i+1];}
function run(file,args){const r=spawnSync(file,args,{cwd:root,encoding:'utf8'});if(r.status!==0)throw Error(`${file} failed: ${(r.stderr??'').slice(0,1500)}`);return r.stdout;}
const privatePath=option('--private-key');
function checkKeyPath(path){if(!path)throw Error('Provide --private-key /private/path/key.pem');const rel=relative(root,resolve(path));if(!rel.startsWith('..'+sep)&&!rel.startsWith('.local'+sep))throw Error('Keep private signing keys outside the source repository. .local is allowed for disposable test keys only.');}
function publicRaw(key){if(key.asymmetricKeyType!=='ed25519')throw Error('An Ed25519 release key is required.');return createPublicKey(key).export({format:'der',type:'spki'}).subarray(-32).toString('base64');}
const domain=Buffer.from('WebDeviceCable release v1\0');
const communityDomain=Buffer.from('WebDeviceCable community checksums v1\0');
const targets=['linux/amd64','linux/arm64','darwin/amd64','darwin/arm64','windows/amd64','windows/arm64'];
async function boundedFile(dir,name,max=256*1024*1024){
 if(!/^[A-Za-z0-9._-]+$/.test(name))throw Error('Unsafe release filename.');
 const path=join(dir,name),info=await lstat(path);
 if(!info.isFile()||info.size>max)throw Error(`Unsafe or oversized release file: ${name}`);
 return readFile(path);
}
async function communityFiles(dir,publicKey){
 const report=JSON.parse(await boundedFile(dir,'build-manifest.json',16384));
 if(report.distribution!=='community'||report.automaticUpdates!==false||report.releasePublicKey!==publicKey||!/^\d+\.\d+\.\d+$/.test(report.version)||!/^[a-f0-9]{40}$/.test(report.sourceCommit))throw Error('Invalid community release manifest.');
 if(JSON.stringify([...report.targets].sort())!==JSON.stringify([...targets].sort()))throw Error('A community release needs all six targets.');
 const expected=[];
 // Older published releases keep their original names and ZIP installers.
 const legacy=report.artifacts.includes(`WebDeviceCable-${report.version}-linux-amd64.tar.gz`);
 for(const target of targets){
  const [os,arch]=target.split('/'),stem=`WebDeviceCable-${report.version}-${os}-${arch}`;
  const download=legacy?stem:downloadStem(report.version,os,arch);
  const name=download+(!legacy&&os==='windows'?'-Setup.exe':os==='linux'?'.tar.gz':'.zip');expected.push(name);
  await boundedFile(dir,name);
  const manifest=inspectArtifact(join(dir,name),stem);
  for(const field of ['version','distribution','sourceCommit','automaticUpdates','releasePublicKey'])if(manifest[field]!==report[field])throw Error(`Inconsistent artifact manifest: ${name} (${field})`);
  if(manifest.target!==target||manifest.protocolVersion!==2||manifest.stateVersion!==1)throw Error(`Incompatible artifact: ${name}`);
  if(os==='darwin')expected.push(download+'.pkg');
 }
 if(JSON.stringify([...report.artifacts].sort())!==JSON.stringify([...expected].sort()))throw Error('Unexpected or missing community artifacts.');
 return {report,files:[...expected,'build-manifest.json','INSTALL.md','RELEASE_PUBLIC_KEY.pem'].sort()};
}
try{
 if(command==='keygen'){
  checkKeyPath(privatePath);const keys=generateKeyPairSync('ed25519');
  await writeFile(resolve(privatePath),keys.privateKey.export({format:'pem',type:'pkcs8'}),{mode:0o600,flag:'wx'});
  console.log(`WDC_RELEASE_PUBLIC_KEY=${publicRaw(keys.privateKey)}`);
 }else if(command==='public-key'){
  checkKeyPath(privatePath);const key=createPrivateKey(await readFile(privatePath));
  console.log(params.includes('--pem')?createPublicKey(key).export({format:'pem',type:'spki'}).trim():publicRaw(key));
 }else if(command==='prepare-community'){
  checkKeyPath(privatePath);const key=createPrivateKey(await readFile(privatePath)),publicKey=publicRaw(key);
  const trusted=createPublicKey(await readFile(join(root,'keys/community-release.pub')));
  if(!trusted.equals(createPublicKey(key)))throw Error('The private key does not match the committed community release key.');
  const dir=resolve(option('--dir',join(root,'dist')));
  await copyFile(join(root,'INSTALL.md'),join(dir,'INSTALL.md'));
  await writeFile(join(dir,'RELEASE_PUBLIC_KEY.pem'),trusted.export({format:'pem',type:'spki'}));
  const {report,files}=await communityFiles(dir,publicKey);
  if(report.version!==JSON.parse(await readFile(join(root,'package.json'),'utf8')).version||report.sourceCommit!==run('git',['rev-parse','HEAD']).trim())throw Error('Release source/version does not match this checkout.');
  if(run('git',['status','--porcelain','--untracked-files=no']).trim())throw Error('Commit tracked source changes before finalizing release artifacts.');
  const lines=[];for(const name of files)lines.push(`${createHash('sha256').update(await boundedFile(dir,name)).digest('hex')}  ${name}\n`);
  const checksums=Buffer.from(lines.join(''));
  await writeFile(join(dir,'SHA256SUMS'),checksums);
  await writeFile(join(dir,'SHA256SUMS.sig'),sign(null,Buffer.concat([communityDomain,checksums]),key));
  console.log(`Community ${report.version}: ${files.length} files signed; manual updates, no publisher identity or notarization. Nothing published.`);
 }else if(command==='verify-community'){
  const dir=resolve(option('--dir',join(root,'dist'))),key=createPublicKey(await readFile(resolve(option('--public-key',join(root,'keys/community-release.pub')))));
  if(key.asymmetricKeyType!=='ed25519')throw Error('An Ed25519 community release key is required.');
  const checksums=await boundedFile(dir,'SHA256SUMS',16384),signature=await boundedFile(dir,'SHA256SUMS.sig',64);
  if(!verify(null,Buffer.concat([communityDomain,checksums]),key,signature))throw Error('Community release signature verification failed.');
  const publicKey=key.export({format:'der',type:'spki'}).subarray(-32).toString('base64');
  const names=[];
  for(const line of checksums.toString('utf8').trimEnd().split('\n')){
   const match=/^([a-f0-9]{64})  ([A-Za-z0-9._-]+)$/.exec(line);
   if(!match||names.includes(match[2]))throw Error('Invalid or duplicate checksum entry.');
   names.push(match[2]);
   if(createHash('sha256').update(await boundedFile(dir,match[2])).digest('hex')!==match[1])throw Error(`Release checksum mismatch: ${match[2]}`);
  }
  const {report,files}=await communityFiles(dir,publicKey);
  if(JSON.stringify(names.sort())!==JSON.stringify(files))throw Error('Release checksum list is incomplete.');
  if(!createPublicKey(await boundedFile(dir,'RELEASE_PUBLIC_KEY.pem',4096)).equals(key))throw Error('Bundled public key differs from the trusted key.');
  console.log(`Verified community ${report.version}: all six targets, installers, source binding, signatures and checksums. Manual updates only.`);
 }else if(command==='prepare'){
  checkKeyPath(privatePath);const key=createPrivateKey(await readFile(privatePath)),publicKey=publicRaw(key);
  const dir=resolve(option('--dir',join(root,'dist'))),version=JSON.parse(await readFile(join(root,'package.json'),'utf8')).version;
  if(!/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(version))throw Error('A stable semantic version is required.');
  const artifacts=[],files=[];
  for(const os of ['linux','darwin','windows'])for(const arch of ['amd64','arm64']){
   const download=downloadStem(version,os,arch);
   const name=download+(os==='windows'?'-Update.zip':os==='linux'?'.tar.gz':'.zip'),file=join(dir,name),data=await boundedFile(dir,name);
   if(data.length>256*1024*1024)throw Error('Release artifact exceeds the update limit.');
   // Inspect only one bounded manifest; never extract untrusted archive entries.
   const manifest=inspectArtifact(file,`WebDeviceCable-${version}-${os}-${arch}`);
   if(manifest.distribution!=='official'||manifest.version!==version||manifest.target!==`${os}/${arch}`||manifest.releasePublicKey!==publicKey)throw Error(`${name} is not an official build with this release key.`);
   if(os==='windows'){
    const setup=download+'-Setup.exe';await boundedFile(dir,setup);
    const installed=inspectArtifact(join(dir,setup),'');
    for(const field of ['version','target','distribution','sourceCommit','automaticUpdates','releasePublicKey','protocolVersion','stateVersion'])if(installed[field]!==manifest[field])throw Error(`Installer/update payload mismatch: ${setup} (${field})`);
    files.push(setup);
   }
   artifacts.push({os,arch,url:`https://github.com/thomasmikava/WebDeviceCable/releases/download/v${version}/${name}`,size:data.length,sha256:createHash('sha256').update(data).digest('hex')});files.push(name);
  }
  if(process.platform!=='darwin')throw Error('Finalize on macOS to generate and verify signed Sparkle feeds.');
  const sparkle=resolve(option('--sparkle-dir',join(root,'.cache/sparkle-2.10.1'))),sparkleKey=option('--sparkle-key-file');
  if(!sparkleKey)throw Error('Provide --sparkle-key-file for signed macOS feeds.');
  await mkdir(join(root,'.local'),{recursive:true});
  for(const arch of ['amd64','arm64']){
   const temporary=await mkdtemp(join(root,'.local/appcast-'));
   try{
    const name=downloadStem(version,'darwin',arch)+'.zip';await copyFile(join(dir,name),join(temporary,name));
    run(join(sparkle,'bin/generate_appcast'),['--ed-key-file',resolve(sparkleKey),'--maximum-deltas','0','--download-url-prefix',`https://github.com/thomasmikava/WebDeviceCable/releases/download/v${version}/`,temporary]);
    const appcast=`appcast-darwin-${arch}.xml`;await copyFile(join(temporary,'appcast.xml'),join(dir,appcast));
    run(join(sparkle,'bin/sign_update'),['--verify','--ed-key-file',resolve(sparkleKey),join(dir,appcast)]);files.push(appcast);
   }finally{await rm(temporary,{recursive:true,force:true});}
  }
  const payload=Buffer.from(JSON.stringify({format:1,version,stateVersion:1,protocolVersion:2,artifacts}));
  const signature=sign(null,Buffer.concat([domain,payload]),key);
  if(!verify(null,Buffer.concat([domain,payload]),createPublicKey(key),signature))throw Error('Release signature verification failed.');
  await writeFile(join(dir,'updates.json'),JSON.stringify({payload:payload.toString('base64'),signature:signature.toString('base64')})+'\n');files.push('updates.json');
  for(const name of await readdir(dir))if(['amd64','arm64'].some(arch=>name===downloadStem(version,'darwin',arch)+'.pkg')||name===`webdevicecable-sdk-${version}.tgz`)files.push(name);
  const lines=[];for(const name of files.sort())lines.push(`${createHash('sha256').update(await readFile(join(dir,name))).digest('hex')}  ${name}\n`);
  await writeFile(join(dir,'SHA256SUMS'),lines.join(''));
  console.log(`WebDeviceCable ${version}: verified metadata and checksums prepared locally. Nothing published.`);
 }else throw Error('Usage: node scripts/release.mjs keygen|public-key|prepare|prepare-community --private-key /private/key.pem; verify-community --dir DIR [--public-key TRUSTED.pem]');
}catch(error){console.error(error.message);process.exitCode=1;}
