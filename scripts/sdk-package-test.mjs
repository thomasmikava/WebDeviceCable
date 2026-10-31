import assert from 'node:assert/strict';
import {mkdtemp,mkdir,rm,writeFile,readFile,readdir,cp} from 'node:fs/promises';
import {resolve} from 'node:path';
import {tmpdir} from 'node:os';
import {spawnSync} from 'node:child_process';
import {createServer} from 'node:http';
import {createContext,SourceTextModule} from 'node:vm';
import {indexedDB} from 'fake-indexeddb';
const dir=await mkdtemp(resolve(tmpdir(),'wdc-sdk-consumer-'));
const npm=process.platform==='win32'?'npm.cmd':'npm';
function run(args,cwd=process.cwd()){const result=spawnSync(npm,args,{cwd,encoding:'utf8',shell:process.platform==='win32'});assert.equal(result.status,0,result.stdout+result.stderr);return result.stdout;}
try{
  run(['run','package:sdk']);
  await writeFile(resolve(dir,'package.json'),JSON.stringify({private:true,type:'module'}));
  run(['install',resolve(`dist/webdevicecable-sdk-${JSON.parse(await readFile(resolve('package.json'),'utf8')).version}.tgz`),resolve(`dist/webdevicecable-relay-${JSON.parse(await readFile(resolve('package.json'),'utf8')).version}.tgz`),'--ignore-scripts','--offline','--cache',resolve('.cache/npm'),'--no-audit'],dir);
  const {version} = JSON.parse(await readFile('package.json','utf8'));
  await mkdir(resolve(dir,'node_modules/@types'),{recursive:true});
  await cp(resolve('node_modules/@types/node'),resolve(dir,'node_modules/@types/node'),{recursive:true});
  await cp(resolve('node_modules/undici-types'),resolve(dir,'node_modules/undici-types'),{recursive:true});
  for (const [name, guide, names] of [
    ['sdk', 'INTEGRATE.md', ['index','secure','storage']],
    ['relay', 'RELAY.md', ['index','secure','storage','relay']],
  ]) {
    const installed = resolve(dir,'node_modules/@webdevicecable',name);
    const manifest = JSON.parse(await readFile(resolve(installed,'package.json'),'utf8'));
    assert.equal(manifest.name, '@webdevicecable/'+name);
    assert.equal(manifest.version, version);
    assert.equal(manifest.publishConfig.access, 'public');
    assert.equal(manifest.repository.url, 'git+https://github.com/thomasmikava/WebDeviceCable.git');
    assert.equal(manifest.private, undefined);
    for (const field of ['scripts','dependencies','optionalDependencies','peerDependencies','bundledDependencies']) {
      assert.ok(!manifest[field] || Object.keys(manifest[field]).length === 0, 'Unexpected package dependency or install script: '+field);
    }
    const expected = ['LICENSE','README.md',guide,'package.json','cjs/package.json'];
    for (const module of names) for (const extension of ['js','d.ts']) {
      expected.push(`${module}.${extension}`,`cjs/${module}.${extension}`);
    }
    const actual = (await readdir(installed,{recursive:true,withFileTypes:true})).filter(entry=>entry.isFile()).map(entry=>
      resolve(entry.parentPath,entry.name).slice(installed.length+1).replaceAll('\\','/'));
    assert.deepEqual(actual.sort(), expected.sort(), 'The archive must contain only reviewed runtime files, types, license and guides');
    const readme = await readFile(resolve(installed,'README.md'),'utf8');
    assert.ok(readme.includes(`@webdevicecable/${name}@${version}`));
    assert.ok(!readme.includes('@VERSION@'));
    assert.ok(!(await readFile(resolve(installed,guide),'utf8')).match(/\]\((?:\.\.?\/|(?:[a-z-]+\.md))/i), 'Packaged guide links must work without a source checkout');
  }
  // Load the installed browser files over HTTP as native modules. The isolated
  // context has browser APIs, but no Node module resolver, process or require.
  const browserRoot = resolve(dir,'node_modules/@webdevicecable/sdk');
  const fetched = new Set();
  const server = createServer(async (request,response)=> {
    const file = request.url.split('/').at(-1);
    if (!['index.js','secure.js','storage.js'].includes(file)) { response.writeHead(404); response.end(); return; }
    fetched.add(file);
    response.setHeader('Content-Type','text/javascript');
    response.end(await readFile(resolve(browserRoot,file)));
  });
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  try {
    const base = `http://127.0.0.1:${server.address().port}/@webdevicecable/sdk@${version}/`;
    const context = createContext({URL,crypto,DOMException,TextEncoder,TextDecoder,AbortController,AbortSignal,
      setTimeout,clearTimeout,indexedDB,location:{origin:'https://fixture.example'}});
    const modules = new Map();
    async function load(url) {
      if (!modules.has(url)) modules.set(url,(async()=> {
        const response = await fetch(url);
        assert.equal(response.status,200);
        return new SourceTextModule(await response.text(),{context,identifier:url});
      })());
      return modules.get(url);
    }
    const entry = await load(base+'index.js');
    await entry.link((specifier,parent)=> {
      assert.ok(specifier.startsWith('./'),'Browser modules must use portable relative imports');
      return load(new URL(specifier,parent.identifier).href);
    });
    await entry.evaluate();
    assert.equal(typeof entry.namespace.BridgeClient.requestPairing,'function');
    assert.equal(typeof entry.namespace.BridgeJobError,'function');
    const account = {appId:'package-fixture',appUser:'alice'};
    const store = new entry.namespace.ConnectionStore(account);
    assert.equal(await store.load(),undefined);
    const key = await crypto.subtle.generateKey({name:'ECDSA',namedCurve:'P-256'},false,['sign','verify']);
    await store.save({pairingId:'a'.repeat(64),privateKey:key.privateKey});
    assert.equal((await store.load()).privateKey.extractable,false);
    assert.equal(await new entry.namespace.ConnectionStore({...account,appUser:'bob'}).load(),undefined);
    await store.clear();
    assert.equal(await store.load(),undefined);
    assert.deepEqual([...fetched].sort(),['index.js','secure.js','storage.js']);
  } finally { await new Promise(resolve=>server.close(resolve)); }
  await writeFile(resolve(dir,'test.mjs'),"import assert from 'node:assert/strict';import {RelayClient} from '@webdevicecable/relay';assert.equal(typeof RelayClient.enroll,'function');import {BridgeClient,ConnectionStore,BridgeSecurityError} from '@webdevicecable/sdk';assert.equal(typeof BridgeClient.requestPairing,'function');assert.equal(typeof ConnectionStore,'function');assert.equal(typeof BridgeSecurityError,'function');");
  const result=spawnSync(process.execPath,['test.mjs'],{cwd:dir,encoding:'utf8'});assert.equal(result.status,0,result.stderr);
  await writeFile(resolve(dir,'test.cjs'),`const assert = require('node:assert/strict');
const { createServer } = require('node:http');
const relay = require('@webdevicecable/relay');
const browser = require('@webdevicecable/sdk');
assert.equal(typeof browser.BridgeClient.requestPairing, 'function');
assert.equal(typeof browser.ConnectionStore, 'function');
assert.throws(() => new browser.ConnectionStore({appId:'fixture', appUser:'alice'}), error => error instanceof browser.BridgeError);
async function main() {
  const esm = await import('@webdevicecable/relay');
  assert.equal(relay.RelayClient, esm.RelayClient);
  assert.equal(relay.RelayAdminClient, esm.RelayAdminClient);
  assert.equal(relay.BridgeError, esm.BridgeError);
  const server = createServer((req, res) => {
    if (req.url.startsWith('/relay/admin/v1/projects')) {
      assert.equal(req.headers.authorization, 'Bearer operator-key');
      res.setHeader('Content-Type', 'application/json');
      const project = {id:'a'.repeat(64),appId:'fixture',name:'Fixture',origin:'https://fixture.example',createdAt:new Date().toISOString(),revoked:req.method==='DELETE'};
      if (req.url === '/relay/admin/v1/projects' && req.method === 'GET') res.end(JSON.stringify({projects:[project]}));
      else if (req.method === 'POST') res.end(JSON.stringify({project,apiKey:'new-fixture-key'}));
      else res.end(JSON.stringify(project));
      return;
    }
    assert.equal(req.headers.authorization, 'Bearer fixture-key');
    assert.equal(req.url, '/relay/v1/enrollments');
    let body = ''; req.on('data', chunk => body += chunk);
    req.on('end', () => {
      res.setHeader('Content-Type', 'application/json');
      if (JSON.parse(body).appUser === 'denied') {
        res.writeHead(403); res.end(JSON.stringify({error:{code:'fixture_denied',message:'Denied'}}));
      } else res.end(JSON.stringify({connectionId:'fixture-connection'}));
    });
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  try {
    const options = {baseURL:'http://127.0.0.1:'+server.address().port,apiKey:'fixture-key'};
    for (const client of [relay, esm]) {
      assert.equal((await client.RelayClient.enroll({...options,appUser:'fixture'})).connectionId,'fixture-connection');
      await assert.rejects(client.RelayClient.enroll({...options,appUser:'denied'}), error => error instanceof relay.BridgeError && error.code === 'fixture_denied');
      const admin = new client.RelayAdminClient({baseURL:options.baseURL,adminKey:'operator-key'});
      const created = await admin.createProject({appId:'fixture',name:'Fixture',origin:'https://fixture.example'});
      assert.equal(created.apiKey,'new-fixture-key');
      assert.equal((await admin.listProjects())[0].id,created.project.id);
      assert.equal((await admin.getProject({projectId:created.project.id})).appId,'fixture');
      assert.equal((await admin.rotateProjectKey({projectId:created.project.id})).apiKey,'new-fixture-key');
      assert.equal((await admin.revokeProject({projectId:created.project.id})).revoked,true);
      assert.throws(()=>admin.getProject({projectId:'../escape'}));
    }
  } finally { await new Promise(resolve => server.close(resolve)); }
}
main().catch(error => { console.error(error); process.exitCode = 1; });
`);
  const commonjs=spawnSync(process.execPath,['test.cjs'],{cwd:dir,encoding:'utf8'});assert.equal(commonjs.status,0,commonjs.stdout+commonjs.stderr);
  for(const extension of ['mts','cts']) await writeFile(resolve(dir,'types.'+extension),"import { RelayClient, RelayAdminClient, type RelayOptions, type RelayProject, type RelayProjectCredential } from '@webdevicecable/relay';\nimport { BridgeClient, ConnectionStore, type ClientOptions } from '@webdevicecable/sdk';\ndeclare const browserOptions: ClientOptions;\nconst browser = new BridgeClient(browserOptions);\nconst store = new ConnectionStore({appId:'fixture',appUser:'alice',origin:'https://fixture.example'});\ndeclare const options: RelayOptions;\nconst client: RelayClient = new RelayClient(options);\nconst admin = new RelayAdminClient({baseURL:options.baseURL,adminKey:'operator-key'});\nconst created: Promise<RelayProjectCredential> = admin.createProject({appId:'fixture',name:'Fixture',origin:'https://fixture.example'});\nconst projects: Promise<RelayProject[]> = admin.listProjects();\nvoid [browser,store,client,created,projects];\n");
  await writeFile(resolve(dir,'tsconfig.json'),JSON.stringify({compilerOptions:{target:'ES2022',module:'NodeNext',moduleResolution:'NodeNext',strict:true,noEmit:true,types:['node']},include:['types.mts','types.cts']}));
  const types=spawnSync(process.execPath,[resolve('node_modules/typescript/bin/tsc'),'-p','tsconfig.json'],{cwd:dir,encoding:'utf8'});assert.equal(types.status,0,types.stdout+types.stderr);
  await writeFile(resolve(dir,'tsconfig.bundler.json'),JSON.stringify({extends:'./tsconfig.json',compilerOptions:{module:'ESNext',moduleResolution:'Bundler'},include:['types.mts']}));
  const bundlerTypes=spawnSync(process.execPath,[resolve('node_modules/typescript/bin/tsc'),'-p','tsconfig.bundler.json'],{cwd:dir,encoding:'utf8'});assert.equal(bundlerTypes.status,0,bundlerTypes.stdout+bundlerTypes.stderr);
  // Node backend declarations must work without DOM libraries. These dev-only
  // types belong to the disposable consumer, not either published package.
  for (const extension of ['mts','cts']) await writeFile(resolve(dir,'node.'+extension),"import { RelayClient, type RelayOptions, type MessageOptions, type ExecutionMetadata } from '@webdevicecable/relay';\ndeclare const options: RelayOptions;\nconst client = new RelayClient(options);\nconst message: MessageOptions = {prompt:'fixture'};\nconst metadata: ExecutionMetadata = {kind:'native'};\nvoid [client,message,metadata];\n");
  await writeFile(resolve(dir,'tsconfig.node.json'),JSON.stringify({compilerOptions:{target:'ES2022',module:'NodeNext',moduleResolution:'NodeNext',strict:true,noEmit:true,lib:['ES2022'],types:['node']},include:['node.mts','node.cts']}));
  const nodeTypes=spawnSync(process.execPath,[resolve('node_modules/typescript/bin/tsc'),'-p','tsconfig.node.json'],{cwd:dir,encoding:'utf8'});assert.equal(nodeTypes.status,0,nodeTypes.stdout+nodeTypes.stderr);
  await rm(resolve(dir,'node_modules/@webdevicecable/sdk'),{recursive:true});
  await writeFile(resolve(dir,'standalone.cjs'),"const assert=require('node:assert/strict'); const relay=require('@webdevicecable/relay'); import('@webdevicecable/relay').then(esm=>{assert.equal(esm.RelayClient,relay.RelayClient); assert.equal(typeof esm.RelayAdminClient,'function');});");
  const standalone=spawnSync(process.execPath,['standalone.cjs'],{cwd:dir,encoding:'utf8'});assert.equal(standalone.status,0,standalone.stdout+standalone.stderr);
  for (const name of ['sdk','relay']) run(['publish',resolve(`dist/webdevicecable-${name}-${version}.tgz`),'--dry-run','--offline','--ignore-scripts','--access','public']);
  console.log('Both reviewed npm archives install, import and typecheck in CommonJS, ESM and bundler resolution; browser HTTP imports and account-scoped key storage pass. Publish dry runs passed; nothing published.');
}finally{await rm(dir,{recursive:true,force:true});}
