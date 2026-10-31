// Local maintainer tool. Does not upload, publish, install, or change Git settings.
import {generateKeyPairSync,createPrivateKey,createPublicKey,sign,verify,createHash} from 'node:crypto';
import {readFile,writeFile,mkdir,copyFile,mkdtemp,rm,readdir} from 'node:fs/promises';
import {resolve,relative,sep,join} from 'node:path';
import {spawnSync} from 'node:child_process';
const root=resolve(import.meta.dirname,'..');
const params=process.argv.slice(2),command=params.shift();
function option(name,fallback){const i=params.indexOf(name);if(i<0)return fallback;if(!params[i+1]||params[i+1].startsWith('--'))throw Error(`${name} needs a value`);return params[i+1];}
function run(file,args){const r=spawnSync(file,args,{cwd:root,encoding:'utf8'});if(r.status!==0)throw Error(`${file} failed: ${(r.stderr??'').slice(0,1500)}`);return r.stdout;}
const privatePath=option('--private-key');
function checkKeyPath(path){if(!path)throw Error('Provide --private-key /private/path/key.pem');const rel=relative(root,resolve(path));if(!rel.startsWith('..'+sep)&&!rel.startsWith('.local'+sep))throw Error('Keep private signing keys outside the source repository. .local is allowed for disposable test keys only.');}
function publicRaw(key){if(key.asymmetricKeyType!=='ed25519')throw Error('An Ed25519 release key is required.');return createPublicKey(key).export({format:'der',type:'spki'}).subarray(-32).toString('base64');}
const domain=Buffer.from('WebDeviceCable release v1\0');
try{
 if(command==='keygen'){
  checkKeyPath(privatePath);const keys=generateKeyPairSync('ed25519');
  await writeFile(resolve(privatePath),keys.privateKey.export({format:'pem',type:'pkcs8'}),{mode:0o600,flag:'wx'});
  console.log(`WDC_RELEASE_PUBLIC_KEY=${publicRaw(keys.privateKey)}`);
 }else if(command==='public-key'){
  checkKeyPath(privatePath);console.log(publicRaw(createPrivateKey(await readFile(privatePath))));
 }else if(command==='prepare'){
  checkKeyPath(privatePath);const key=createPrivateKey(await readFile(privatePath)),publicKey=publicRaw(key);
  const dir=resolve(option('--dir',join(root,'dist'))),version=JSON.parse(await readFile(join(root,'package.json'),'utf8')).version;
  if(!/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(version))throw Error('A stable semantic version is required.');
  const artifacts=[],files=[];
  for(const os of ['linux','darwin','windows'])for(const arch of ['amd64','arm64']){
   const name=`WebDeviceCable-${version}-${os}-${arch}.${os==='linux'?'tar.gz':'zip'}`,file=join(dir,name),data=await readFile(file);
   if(data.length>256*1024*1024)throw Error('Release artifact exceeds the update limit.');
   // Inspect only one bounded manifest; never extract untrusted archive entries.
   const inspect=`import sys,json,zipfile,tarfile\np=sys.argv[1];name=sys.argv[2]\nif p.endswith('.zip'):\n z=zipfile.ZipFile(p);i=z.getinfo(name);assert i.file_size<16384;data=z.read(name)\nelse:\n z=tarfile.open(p);i=z.getmember(name);assert i.isfile() and i.size<16384;data=z.extractfile(i).read()\nprint(data.decode())`;
   const manifest=JSON.parse(run(process.platform==='win32'?'python':'python3',['-c',inspect,file,`WebDeviceCable-${version}-${os}-${arch}/build-manifest.json`]));
   if(manifest.distribution!=='official'||manifest.version!==version||manifest.target!==`${os}/${arch}`||manifest.releasePublicKey!==publicKey)throw Error(`${name} is not an official build with this release key.`);
   artifacts.push({os,arch,url:`https://github.com/thomasmikava/WebDeviceCable/releases/download/v${version}/${name}`,size:data.length,sha256:createHash('sha256').update(data).digest('hex')});files.push(name);
  }
  if(process.platform!=='darwin')throw Error('Finalize on macOS to generate and verify signed Sparkle feeds.');
  const sparkle=resolve(option('--sparkle-dir',join(root,'.cache/sparkle-2.10.1'))),sparkleKey=option('--sparkle-key-file');
  if(!sparkleKey)throw Error('Provide --sparkle-key-file for signed macOS feeds.');
  await mkdir(join(root,'.local'),{recursive:true});
  for(const arch of ['amd64','arm64']){
   const temporary=await mkdtemp(join(root,'.local/appcast-'));
   try{
    const name=`WebDeviceCable-${version}-darwin-${arch}.zip`;await copyFile(join(dir,name),join(temporary,name));
    run(join(sparkle,'bin/generate_appcast'),['--ed-key-file',resolve(sparkleKey),'--maximum-deltas','0','--download-url-prefix',`https://github.com/thomasmikava/WebDeviceCable/releases/download/v${version}/`,temporary]);
    const appcast=`appcast-darwin-${arch}.xml`;await copyFile(join(temporary,'appcast.xml'),join(dir,appcast));
    run(join(sparkle,'bin/sign_update'),['--verify','--ed-key-file',resolve(sparkleKey),join(dir,appcast)]);files.push(appcast);
   }finally{await rm(temporary,{recursive:true,force:true});}
  }
  const payload=Buffer.from(JSON.stringify({format:1,version,stateVersion:1,protocolVersion:2,artifacts}));
  const signature=sign(null,Buffer.concat([domain,payload]),key);
  if(!verify(null,Buffer.concat([domain,payload]),createPublicKey(key),signature))throw Error('Release signature verification failed.');
  await writeFile(join(dir,'updates.json'),JSON.stringify({payload:payload.toString('base64'),signature:signature.toString('base64')})+'\n');files.push('updates.json');
  for(const name of await readdir(dir))if(name===`WebDeviceCable-${version}-darwin-arm64.pkg`||name===`WebDeviceCable-${version}-darwin-amd64.pkg`||name===`webdevicecable-sdk-${version}.tgz`)files.push(name);
  const lines=[];for(const name of files.sort())lines.push(`${createHash('sha256').update(await readFile(join(dir,name))).digest('hex')}  ${name}\n`);
  await writeFile(join(dir,'SHA256SUMS'),lines.join(''));
  console.log(`WebDeviceCable ${version}: verified metadata and checksums prepared locally. Nothing published.`);
 }else throw Error('Usage: node scripts/release.mjs keygen|public-key|prepare --private-key /private/key.pem');
}catch(error){console.error(error.message);process.exitCode=1;}
