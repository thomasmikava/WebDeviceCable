import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import vm from 'node:vm';
import { BuildError, TARGETS, buildSite, extractSdkExample, highlightJs, validateRelease } from './site-build.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const fixturePath = path.join(root, 'scripts/testdata/site-release.json');
const fixture = () => JSON.parse(readFileSync(fixturePath, 'utf8'));
const downloadNames = JSON.parse(readFileSync(path.join(root, 'scripts/download-names.json'), 'utf8'));
const siteJs = readFileSync(path.join(root, 'site/site.js'), 'utf8');

function loadSite(context = {}) {
  vm.createContext(context);
  vm.runInContext(siteJs, context, { filename: 'site/site.js' });
  return context.WDCSite;
}
const { detectPlatform } = loadSite();

const UA = {
  macChrome: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36',
  macSafari: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.4 Safari/605.1.15',
  macFirefox: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:140.0) Gecko/20100101 Firefox/140.0',
  winChrome: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36',
  winFirefox: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:140.0) Gecko/20100101 Firefox/140.0',
  winArmOld: 'Mozilla/5.0 (Windows NT 10.0; ARM64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/100.0.0.0 Safari/537.36',
  linuxFirefox: 'Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0',
  linuxArmFirefox: 'Mozilla/5.0 (X11; Linux aarch64; rv:140.0) Gecko/20100101 Firefox/140.0',
  linux32: 'Mozilla/5.0 (X11; Linux i686; rv:140.0) Gecko/20100101 Firefox/140.0',
  linuxChrome: 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36',
  iphone: 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_4 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.4 Mobile/15E148 Safari/604.1',
  android: 'Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Mobile Safari/537.36',
  androidFirefox: 'Mozilla/5.0 (Android 15; Mobile; rv:140.0) Gecko/140.0 Firefox/140.0',
  chromebook: 'Mozilla/5.0 (X11; CrOS x86_64 14541.0.0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36',
  googlebot: 'Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; Googlebot/2.1; +http://www.google.com/bot.html) Chrome/141.0.0.0 Safari/537.36',
  freebsd: 'Mozilla/5.0 (X11; FreeBSD amd64; rv:140.0) Gecko/20100101 Firefox/140.0',
};
const ch = (platform) => ({ platform, mobile: false });

const cases = [
  ['Chrome on Apple silicon', { userAgent: UA.macChrome, platform: 'MacIntel', uaData: ch('macOS'), hints: { architecture: 'arm', bitness: '64' } }, 'darwin', 'darwin/arm64', 'high'],
  ['Chrome on an Intel Mac', { userAgent: UA.macChrome, platform: 'MacIntel', uaData: ch('macOS'), hints: { architecture: 'x86', bitness: '64' } }, 'darwin', 'darwin/amd64', 'high'],
  ['Chrome on Mac before hints load', { userAgent: UA.macChrome, platform: 'MacIntel', uaData: ch('macOS') }, 'darwin', 'darwin/arm64', 'pending'],
  ['Chrome on Mac when hints fail', { userAgent: UA.macChrome, platform: 'MacIntel', uaData: ch('macOS'), hints: null }, 'darwin', 'darwin/arm64', 'low'],
  ['Safari on Mac', { userAgent: UA.macSafari, platform: 'MacIntel', maxTouchPoints: 0 }, 'darwin', 'darwin/arm64', 'low'],
  ['Firefox on Mac', { userAgent: UA.macFirefox, platform: 'MacIntel' }, 'darwin', 'darwin/arm64', 'low'],
  ['iPad posing as a Mac', { userAgent: UA.macSafari, platform: 'MacIntel', maxTouchPoints: 5 }, 'ipados', null, 'none'],
  ['iPhone', { userAgent: UA.iphone, platform: 'iPhone', maxTouchPoints: 5 }, 'ios', null, 'none'],
  ['Android Chrome', { userAgent: UA.android, platform: 'Linux armv81', uaData: { platform: 'Android', mobile: true } }, 'android', null, 'none'],
  ['Android Firefox', { userAgent: UA.androidFirefox, platform: 'Linux aarch64' }, 'android', null, 'none'],
  ['Chromebook', { userAgent: UA.chromebook, platform: 'Linux x86_64', uaData: ch('Chrome OS') }, 'chromeos', null, 'none'],
  ['Chrome on Windows x64', { userAgent: UA.winChrome, platform: 'Win32', uaData: ch('Windows'), hints: { architecture: 'x86', bitness: '64' } }, 'windows', 'windows/amd64', 'high'],
  ['Chrome on Windows on Arm (frozen x64 UA)', { userAgent: UA.winChrome, platform: 'Win32', uaData: ch('Windows'), hints: { architecture: 'arm', bitness: '64' } }, 'windows', 'windows/arm64', 'high'],
  ['Firefox on Windows', { userAgent: UA.winFirefox, platform: 'Win32' }, 'windows', 'windows/amd64', 'low'],
  ['Old browser on Windows on Arm', { userAgent: UA.winArmOld, platform: 'Win32' }, 'windows', 'windows/arm64', 'high'],
  ['Firefox on Linux x64', { userAgent: UA.linuxFirefox, platform: 'Linux x86_64' }, 'linux', 'linux/amd64', 'high'],
  ['Firefox on Linux Arm64', { userAgent: UA.linuxArmFirefox, platform: 'Linux aarch64' }, 'linux', 'linux/arm64', 'high'],
  ['Chromium on Linux Arm64 (frozen x64 UA)', { userAgent: UA.linuxChrome, platform: 'Linux x86_64', uaData: ch('Linux'), hints: { architecture: 'arm', bitness: '64' } }, 'linux', 'linux/arm64', 'high'],
  ['32-bit Linux', { userAgent: UA.linux32, platform: 'Linux i686' }, 'linux', null, 'none'],
  ['Googlebot', { userAgent: UA.googlebot }, 'unknown', null, 'none'],
  ['FreeBSD', { userAgent: UA.freebsd, platform: 'FreeBSD amd64' }, 'unknown', null, 'none'],
  ['Empty input', {}, 'unknown', null, 'none'],
  ['Garbage input', { userAgent: 42, platform: null, maxTouchPoints: 'x', uaData: 'nope', hints: 7 }, 'unknown', null, 'none'],
];

for (const [name, env, os, target, confidence] of cases) {
  test(`detectPlatform: ${name}`, () => {
    const result = detectPlatform(env);
    assert.equal(result.os, os);
    assert.equal(result.target, target);
    assert.equal(result.confidence, confidence);
  });
}

test('every detected target has a download', () => {
  for (const [, env] of cases) {
    const { target } = detectPlatform(env);
    if (target) assert.ok(target in downloadNames, `${target} is not in download-names.json`);
  }
});

function fakeDocument() {
  const attributes = {};
  const listeners = {};
  return {
    attributes,
    listeners,
    documentElement: { setAttribute: (name, value) => { attributes[name] = value; } },
    addEventListener: (type, fn) => { listeners[type] = fn; },
    getElementById: () => null,
  };
}

test('the page script settles on the browser-reported chip', async () => {
  const document = fakeDocument();
  let resolveHints;
  const navigator = {
    userAgent: UA.macChrome, platform: 'MacIntel', maxTouchPoints: 0,
    userAgentData: { platform: 'macOS', mobile: false, getHighEntropyValues: () => new Promise((resolve) => { resolveHints = resolve; }) },
  };
  loadSite({ document, navigator, setTimeout });
  assert.deepEqual(document.attributes, { 'data-os': 'darwin', 'data-target': 'darwin-arm64', 'data-arch-confidence': 'pending' });
  resolveHints({ architecture: 'x86', bitness: '64' });
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(document.attributes, { 'data-os': 'darwin', 'data-target': 'darwin-amd64', 'data-arch-confidence': 'high' });
  assert.equal(typeof document.listeners.click, 'function');
});

test('the page script falls back when hints are refused', async () => {
  const document = fakeDocument();
  const navigator = {
    userAgent: UA.winChrome, platform: 'Win32',
    userAgentData: { platform: 'Windows', mobile: false, getHighEntropyValues: () => Promise.reject(new Error('blocked')) },
  };
  loadSite({ document, navigator, setTimeout });
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(document.attributes, { 'data-os': 'windows', 'data-target': 'windows-amd64', 'data-arch-confidence': 'low' });
});

test('phones get the computer-only message', () => {
  const document = fakeDocument();
  loadSite({ document, navigator: { userAgent: UA.iphone, platform: 'iPhone', maxTouchPoints: 5 }, setTimeout });
  assert.equal(document.attributes['data-target'], 'unsupported');
});

test('the page script makes no requests and stores nothing', () => {
  const code = siteJs.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '');
  for (const pattern of [/fetch\s*\(/, /XMLHttpRequest/, /sendBeacon/, /cookie/i, /localStorage|sessionStorage|indexedDB/, /https?:\/\//, /location\s*[.=]/]) {
    assert.doesNotMatch(code, pattern);
  }
});

// ---------- Build ----------

const build = (overrides = {}) => buildSite({ release: fixture(), ...overrides });
const text = (files, name) => files.get(name).toString('utf8');

test('the build ships exactly the expected files', () => {
  const { files } = build();
  assert.deepEqual([...files.keys()].sort(), [
    '404.html',
    'assets/favicon.svg', 'assets/icons.svg', 'assets/logo.svg', 'assets/og.png',
    'assets/site.css', 'assets/site.js', 'assets/theme.css', 'assets/tokens.css',
    'developers/index.html', 'index.html', 'robots.txt', 'sitemap.xml',
  ]);
});

test('the home page links every installer of the release', () => {
  const { files } = build();
  const html = text(files, 'index.html');
  const release = fixture();
  for (const key of Object.keys(TARGETS)) {
    const name = `WebDeviceCable-0.2.5-${downloadNames[key]}${TARGETS[key].ext}`;
    const url = release.assets.find((asset) => asset.name === name).browser_download_url;
    assert.ok(html.split(`href="${url}"`).length - 1 >= 3, `${name} should be linked from both buttons and the list`);
    assert.match(html, new RegExp(`data-for="${key.replace('/', '-')}" hidden`));
  }
  assert.match(html, /href="https:\/\/github\.com\/thomasmikava\/WebDeviceCable\/releases\/download\/v0\.2\.5\/SHA256SUMS"/);
  assert.match(html, /Version 0\.2\.5/);
  assert.match(html, /data-for="unsupported" hidden/);
});

test('a guessed chip offers the other download', () => {
  const html = text(build().files, 'index.html');
  const url = (file) => `https://github.com/thomasmikava/WebDeviceCable/releases/download/v0.2.5/WebDeviceCable-0.2.5-${file}`;
  const variant = (key) => new RegExp(`data-for="${key}" hidden>([\\s\\S]*?)</div>`).exec(html)[1];
  for (const [key, file, label] of [
    ['darwin-arm64', 'macOS-Intel.pkg', 'Intel Mac?'],
    ['windows-amd64', 'Windows-ARM64-Setup.exe', 'Windows on Arm?'],
    ['linux-amd64', 'Linux-ARM64.tar.gz', 'Linux on Arm?'],
  ]) {
    const inner = variant(key);
    assert.ok(inner.includes(`<a class="alt-arch" href="${url(file)}" data-download>${label}`), key);
  }
  for (const key of ['darwin-amd64', 'windows-arm64', 'linux-arm64']) assert.doesNotMatch(variant(key), /alt-arch/, key);
});

test('without JavaScript the page shows a working default', () => {
  const html = text(build().files, 'index.html');
  assert.match(html, /<div class="cta-variant" data-for="default">/);
  assert.doesNotMatch(html, /data-for="default" hidden/);
  assert.match(html, /<a class="button primary large" href="#downloads">See downloads<\/a>/);
  assert.match(html, /id="downloads"/);
  assert.equal((html.match(/class="download-row"/g) ?? []).length, 6);
});

test('the developers page shows both code examples, coloured', () => {
  const html = text(build().files, 'developers/index.html');
  const sdk = extractSdkExample(readFileSync(path.join(root, 'sdk/README.md'), 'utf8'));
  const relay = readFileSync(path.join(root, 'site/examples/relay.js'), 'utf8').trimEnd();
  assert.ok(html.includes(highlightJs(sdk)), 'SDK example missing');
  assert.ok(html.includes(highlightJs(relay)), 'relay example missing');
  assert.match(html, /<span class="tok-keyword">await<\/span>/);
  assert.match(html, /npm install @webdevicecable\/sdk</);
});

test('code colouring keeps the code text unchanged', () => {
  const code = "// note <b>\nconst a = await new Foo({ x: 'it\\'s', n: 42 }).run(`t`);\nif (a) throw a;";
  const html = highlightJs(code);
  const back = html.replace(/<[^>]+>/g, '').replaceAll('&#123;', '{').replaceAll('&#125;', '}')
    .replaceAll('&lt;', '<').replaceAll('&gt;', '>').replaceAll('&quot;', '"').replaceAll('&#39;', "'").replaceAll('&amp;', '&');
  assert.equal(back, code);
  assert.doesNotMatch(html, /[{}]/);
  for (const kind of ['comment', 'keyword', 'type', 'string', 'number', 'call']) assert.match(html, new RegExp(`class="tok-${kind}"`));
});

test('the server example uses the real relay client API', () => {
  const example = readFileSync(path.join(root, 'site/examples/relay.js'), 'utf8');
  const relayTs = readFileSync(path.join(root, 'sdk/relay.ts'), 'utf8');
  const indexTs = readFileSync(path.join(root, 'sdk/index.ts'), 'utf8');
  assert.match(example, /^import \{ RelayClient, relayConnectURL \} from '@webdevicecable\/relay';/);
  assert.match(relayTs, /export class RelayClient\b/);
  assert.match(relayTs, /export function relayConnectURL\(/);
  assert.match(relayTs, /static enroll\(/);
  const options = /export interface RelayOptions \{([\s\S]*?)\n\}/.exec(relayTs)[1];
  for (const key of ['baseURL', 'apiKey', 'connectionId', 'origin', 'account', 'groups']) assert.match(options, new RegExp(`\\b${key}\\??:`), key);
  const enrollment = /export interface RelayEnrollment \{([^}]*)\}/.exec(relayTs)[1];
  for (const key of ['code', 'connectionId', 'appId', 'origin']) assert.match(enrollment, new RegExp(`\\b${key}:`), key);
  for (const method of ['connect', 'run', 'close']) {
    assert.match(example, new RegExp(`client\\.${method}\\(`));
    assert.match(indexTs, new RegExp(`^  async ${method}\\(`, 'm'), method);
  }
});

test('the developers page presents both ways to connect', () => {
  const html = text(build().files, 'developers/index.html');
  assert.match(html, /<h3 id="browser-title">From your web page<\/h3>/);
  assert.match(html, /<h3 id="server-title">From your server<\/h3>/);
  assert.match(html, /npm install @webdevicecable\/relay</);
  assert.match(html, /docs\/integrate\.md">Web page guide</);
  assert.match(html, /docs\/relay\.md">Server and relay guide</);
});

test('the relay diagram shows the user’s computer reaching out to the relay', () => {
  const html = text(build().files, 'developers/index.html');
  const diagram = html.match(/<div class="diagram diagram-3"[\s\S]*?node-user[^<]*<\/span>\s*<\/div>/)?.[0];
  assert.ok(diagram, 'relay diagram missing');
  // Boxes left to right: your server, your relay, user's computer. Between the relay and the
  // computer: the computer asks (to-left), the relay hands over the request (to-right), and
  // the answer goes back (to-left).
  const lanes = (part) => [...part.matchAll(/<span class="lane (to-\w+)"><span class="lane-label">([^<]+)</g)].map((m) => `${m[1]} ${m[2]}`);
  const [server, relay] = diagram.split('<span class="node r-relay">');
  assert.deepEqual(lanes(server), ['to-right request', 'to-left answer']);
  assert.deepEqual(lanes(relay), ['to-left asks for work', 'to-right request', 'to-left answer']);
  assert.doesNotMatch(html, /class="flow"/);
});

test('every diagram line carries a message dot with its own animation', () => {
  const html = text(build().files, 'developers/index.html');
  const css = readFileSync(path.join(root, 'site/site.css'), 'utf8');
  const lanes = [...html.matchAll(/<span class="lane to-\w+">[\s\S]*?<\/span><\/span>/g)].map((m) => m[0]);
  assert.equal(lanes.length, 7);
  for (const lane of lanes) {
    const name = /<span class="dot[^"]* ([br]-\w+)"><\/span>/.exec(lane)?.[1];
    assert.ok(name, `no dot in ${lane}`);
    assert.match(css, new RegExp(`@keyframes ${name} \\{`));
    assert.match(css, new RegExp(`\\.${name} \\{ animation-name: ${name}; \\}`));
  }
  assert.match(css, /\.dot \{ display: none;/, 'dots must stay hidden when motion is reduced');
});

test('links work under a project subpath', () => {
  const { files } = build();
  for (const [name, content] of files) {
    if (!/\.(html|css)$/.test(name)) continue;
    const body = content.toString('utf8').replace(/<base href="[^"]*">/, '');
    assert.doesNotMatch(body, /\s(href|src)="\/(?!\/)/, `${name} has a root-absolute link`);
    assert.doesNotMatch(body, /url\(\s*['"]?\//, `${name} has a root-absolute url()`);
    assert.doesNotMatch(body, /http:\/\//, `${name} has an insecure link`);
  }
  assert.match(text(files, 'developers/index.html'), /href="\.\.\/assets\/site\.css\?v=[0-9a-f]{10}"/);
  assert.match(text(files, '404.html'), /<base href="\/WebDeviceCable\/">/);
});

test('a custom domain needs no page changes', () => {
  const { files } = build({ siteUrl: 'https://example.test' });
  assert.match(text(files, '404.html'), /<base href="\/">/);
  assert.match(text(files, 'index.html'), /<link rel="canonical" href="https:\/\/example\.test\/">/);
  assert.match(text(files, 'index.html'), /content="https:\/\/example\.test\/assets\/og\.png"/);
  assert.match(text(files, 'sitemap.xml'), /<loc>https:\/\/example\.test\/developers\/<\/loc>/);
});

test('the colours come only from the panel', () => {
  const { files } = build();
  assert.ok(files.get('assets/theme.css').equals(readFileSync(path.join(root, 'internal/bridge/web/theme.css'))));
  const tokens = text(files, 'assets/tokens.css');
  const styleCss = readFileSync(path.join(root, 'internal/bridge/web/style.css'), 'utf8');
  assert.ok(tokens.includes(/:root\s*\{[^}]*\}/.exec(styleCss)[0]));
  const siteCss = readFileSync(path.join(root, 'site/site.css'), 'utf8');
  assert.doesNotMatch(siteCss.replace(/\/\*[\s\S]*?\*\//g, ''), /#[0-9a-f]{3,8}\b|rgba?\(|hsla?\(/i);
  const defined = new Set([...(text(files, 'assets/theme.css') + tokens + siteCss).matchAll(/(--[\w-]+)\s*:/g)].map((m) => m[1]));
  for (const [, name] of siteCss.matchAll(/var\((--[\w-]+)/g)) assert.ok(defined.has(name), `${name} is not defined`);
});

test('pages load nothing from other sites', () => {
  for (const [name, content] of build().files) {
    if (!name.endsWith('.html')) continue;
    const html = content.toString('utf8');
    assert.doesNotMatch(html, /<(?:script|img|iframe)[^>]+src="https?:/, name);
    assert.doesNotMatch(html, /<link[^>]+rel="(?:stylesheet|preload|icon)"[^>]+href="https?:/, name);
    assert.doesNotMatch(html, /claude-icon|chatgpt-icon/, name);
  }
});

test('two builds are identical', () => {
  const a = build().files;
  const b = build().files;
  for (const [name, content] of a) assert.ok(content.equals(b.get(name)), name);
});

const broken = {
  'a missing installer': (r) => { r.assets = r.assets.filter((a) => !a.name.endsWith('Windows-ARM64-Setup.exe')); },
  'a pre-release': (r) => { r.prerelease = true; },
  'a draft': (r) => { r.draft = true; },
  'an npm release tag': (r) => { r.tag_name = 'npm-v0.2.5'; },
  'a download on another host': (r) => { r.assets[5].browser_download_url = 'https://example.com/x.tar.gz'; },
  'an unfinished upload': (r) => { r.assets.find((a) => a.name.endsWith('.pkg')).state = 'starter'; },
  'no checksums': (r) => { r.assets = r.assets.filter((a) => a.name !== 'SHA256SUMS'); },
};
for (const [name, breakIt] of Object.entries(broken)) {
  test(`the build refuses ${name}`, () => {
    const release = fixture();
    breakIt(release);
    assert.throws(() => validateRelease(release, downloadNames), BuildError);
  });
}

test('the command line writes nothing when the release is broken', () => {
  const dir = mkdtempSync(path.join(os.tmpdir(), 'site-build-'));
  try {
    const bad = fixture();
    bad.prerelease = true;
    writeFileSync(path.join(dir, 'bad.json'), JSON.stringify(bad));
    const out = path.join(dir, 'out');
    const failed = spawnSync(process.execPath, ['scripts/site-build.mjs', '--release-json', path.join(dir, 'bad.json'), '--out', out], { cwd: root, encoding: 'utf8' });
    assert.equal(failed.status, 1);
    assert.match(failed.stderr, /pre-release/);
    assert.equal(existsSync(out), false);

    const ok = spawnSync(process.execPath, ['scripts/site-build.mjs', '--release-json', fixturePath, '--out', out], { cwd: root, encoding: 'utf8' });
    assert.equal(ok.status, 0, ok.stderr);
    assert.ok(readdirSync(out).includes('index.html'));

    const stranger = path.join(dir, 'stranger');
    mkdirSync(stranger);
    writeFileSync(path.join(stranger, 'keep.txt'), 'x');
    const refused = spawnSync(process.execPath, ['scripts/site-build.mjs', '--release-json', fixturePath, '--out', stranger], { cwd: root, encoding: 'utf8' });
    assert.equal(refused.status, 1);
    assert.ok(existsSync(path.join(stranger, 'keep.txt')));
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
