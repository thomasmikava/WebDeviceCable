// Local artifacts only. This script never publishes or uses registry credentials.
import {mkdir, readFile, writeFile, copyFile, rm, cp} from 'node:fs/promises';
import {resolve} from 'node:path';
import {spawnSync} from 'node:child_process';

const root = resolve(import.meta.dirname, '..');
const out = resolve(root, 'dist');
const {version} = JSON.parse(await readFile(resolve(root, 'package.json'), 'utf8'));
const repository = 'https://github.com/thomasmikava/WebDeviceCable';
const releaseDocs = `${repository}/blob/npm-v${version}/docs/`;
const modules = ['index', 'secure', 'storage'];
const compiled = resolve(out, '.sdk-cjs');
const metadata = {
  version,
  type: 'module',
  license: 'MIT',
  repository: {type: 'git', url: `git+${repository}.git`},
  homepage: `${repository}#readme`,
  bugs: {url: `${repository}/issues`},
  publishConfig: {access: 'public', registry: 'https://registry.npmjs.org/'},
  sideEffects: false,
};

function run(file, args, cwd = root) {
  const result = spawnSync(file, args, {cwd, stdio: 'inherit', shell: process.platform === 'win32' && file.endsWith('.cmd')});
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${file} exited with ${result.status}`);
}

async function copyModules(names, from, to) {
  await mkdir(to, {recursive: true});
  for (const name of names) {
    for (const extension of ['js', 'd.ts']) {
      await copyFile(resolve(from, `${name}.${extension}`), resolve(to, `${name}.${extension}`));
    }
  }
}

async function writePackage(directory, manifest, readme, guide) {
  await writeFile(resolve(directory, 'package.json'), JSON.stringify({...metadata, ...manifest}, null, 2) + '\n');
  await copyFile(resolve(root, 'LICENSE'), resolve(directory, 'LICENSE'));
  const readmeText = await readFile(resolve(root, 'sdk', readme), 'utf8');
  await writeFile(resolve(directory, 'README.md'), readmeText.replaceAll('@VERSION@', version).replaceAll(`${repository}/blob/HEAD/docs/`, releaseDocs));
  // Package guides retain working links when read outside the source checkout.
  const contents = await readFile(resolve(root, 'docs', guide), 'utf8');
  const linked = contents.replace(/\]\((?![a-z]+:|#)([^)]+)\)/gi, (_, target) =>
    `](${new URL(target, releaseDocs).href})`);
  await writeFile(resolve(directory, guide === 'integrate.md' ? 'INTEGRATE.md' : 'RELAY.md'), linked);
  run(process.platform === 'win32' ? 'npm.cmd' : 'npm', [
    'pack', '--ignore-scripts', '--offline', '--cache', resolve(root, '.cache/npm'), '--pack-destination', out,
  ], directory);
}

await rm(compiled, {recursive: true, force: true});
try {
  run(process.execPath, [
    resolve(root, 'node_modules/typescript/bin/tsc'), '-p', resolve(root, 'sdk/tsconfig.json'),
    '--module', 'CommonJS', '--moduleResolution', 'Node', '--outDir', compiled,
  ]);
  await writeFile(resolve(compiled, 'package.json'), JSON.stringify({type: 'commonjs'}) + '\n');

  const browser = resolve(out, 'sdk');
  await rm(browser, {recursive: true, force: true});
  await copyModules(modules, resolve(root, 'internal/bridge/web'), browser);
  await copyModules(modules, compiled, resolve(browser, 'cjs'));
  await copyFile(resolve(compiled, 'package.json'), resolve(browser, 'cjs/package.json'));
  await writePackage(browser, {
    name: '@webdevicecable/sdk',
    description: 'WebDeviceCable browser SDK for direct, encrypted connections to local desktop clients',
    keywords: ['webdevicecable', 'wdc', 'browser', 'local-ai', 'sdk'],
    main: './cjs/index.js',
    module: './index.js',
    browser: './index.js',
    types: './index.d.ts',
    unpkg: './index.js',
    jsdelivr: './index.js',
    exports: {
      '.': {
        import: {types: './index.d.ts', default: './index.js'},
        require: {types: './cjs/index.d.ts', default: './cjs/index.js'},
        default: './index.js',
      },
      './index.js': './index.js',
      './package.json': './package.json',
    },
    files: ['index.*', 'secure.*', 'storage.*', 'cjs', 'LICENSE', 'README.md', 'INTEGRATE.md'],
  }, 'README.md', 'integrate.md');

  const backend = resolve(out, 'relay-client');
  await rm(backend, {recursive: true, force: true});
  await copyModules([...modules, 'relay'], resolve(root, 'internal/bridge/web'), backend);
  await cp(compiled, resolve(backend, 'cjs'), {recursive: true});
  // Node-only TypeScript projects do not include the DOM CryptoKey global.
  for (const directory of [backend, resolve(backend, 'cjs')]) {
    const file = resolve(directory, 'secure.d.ts');
    await writeFile(file, "import type { webcrypto } from 'node:crypto';\ntype CryptoKey = webcrypto.CryptoKey;\n" + await readFile(file, 'utf8'));
  }
  // Node ESM and CommonJS share one runtime, including error class identity.
  await writeFile(resolve(backend, 'relay.js'), "import relay from './cjs/relay.js';\nexport const { RelayClient, RelayAdminClient, BridgeError, BridgeJobError, relayRequest } = relay;\n");
  await writePackage(backend, {
    name: '@webdevicecable/relay',
    description: 'WebDeviceCable relay client for Node backends and dynamic project provisioning',
    keywords: ['webdevicecable', 'wdc', 'relay', 'local-ai', 'backend'],
    engines: {node: '>=22'},
    main: './cjs/relay.js',
    module: './relay.js',
    types: './relay.d.ts',
    exports: {
      '.': {
        import: {types: './relay.d.ts', default: './relay.js'},
        require: {types: './cjs/relay.d.ts', default: './cjs/relay.js'},
        default: './relay.js',
      },
      './package.json': './package.json',
    },
    files: ['*.js', '*.d.ts', 'cjs', 'LICENSE', 'README.md', 'RELAY.md'],
  }, 'relay.README.md', 'relay.md');
} finally {
  await rm(compiled, {recursive: true, force: true});
}
