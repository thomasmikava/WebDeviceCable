#!/usr/bin/env node
// Builds the public website (site/) into a static folder for GitHub Pages.
//
//   node scripts/site-build.mjs [--out dist/site] [--release-json FILE] [--site-url URL]
//
// Download links are baked into the HTML from the latest stable GitHub release, so the
// published pages never call the GitHub API. Colours, logo and icons are copied from the
// desktop control panel (internal/bridge/web) so the site cannot drift from the app.
// Every check runs before anything is written; on any failure the script exits 1 and
// leaves the output folder untouched.
import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseArgs } from 'node:util';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
export const REPO = 'thomasmikava/WebDeviceCable';
export const REPO_URL = `https://github.com/${REPO}`;
export const DEFAULT_SITE_URL = 'https://thomasmikava.github.io/WebDeviceCable';

// Display names for each download. Keys must match scripts/download-names.json.
export const TARGETS = {
  'darwin/arm64': { os: 'darwin', osName: 'Mac', chip: 'Apple Silicon', plain: 'Mac with Apple silicon (M1 or later)', ext: '.pkg' },
  'darwin/amd64': { os: 'darwin', osName: 'Mac', chip: 'Intel', plain: 'Mac with an Intel processor', ext: '.pkg' },
  'windows/amd64': { os: 'windows', osName: 'Windows', chip: 'x64', plain: 'Windows on Intel or AMD (most PCs)', ext: '-Setup.exe' },
  'windows/arm64': { os: 'windows', osName: 'Windows', chip: 'Arm64', plain: 'Windows on Arm (for example, Snapdragon)', ext: '-Setup.exe' },
  'linux/amd64': { os: 'linux', osName: 'Linux', chip: 'x64', plain: 'Linux on Intel or AMD', ext: '.tar.gz' },
  'linux/arm64': { os: 'linux', osName: 'Linux', chip: 'Arm64', plain: 'Linux on Arm64', ext: '.tar.gz' },
};

// When the browser does not report the chip, the button is a guess (Apple Silicon on a Mac,
// x64 elsewhere), so it also offers the other chip's download.
const ALT_ARCH = {
  'darwin/arm64': { key: 'darwin/amd64', text: 'Intel Mac? Download the Intel version' },
  'windows/amd64': { key: 'windows/arm64', text: 'Windows on Arm? Download the Arm version' },
  'linux/amd64': { key: 'linux/arm64', text: 'Linux on Arm? Download the Arm64 version' },
};

// Files copied from the control panel. An explicit list: nothing else from the app ships.
const PANEL_FILES = ['theme.css', 'logo.svg', 'favicon.svg', 'icons.svg'];
const SITE_ASSETS = ['site.css', 'site.js', 'og.png'];

const PAGES = [
  {
    src: 'index.html',
    title: 'WebDeviceCable: let websites use the AI tools on your computer',
    description: 'WebDeviceCable lets websites you approve use the AI tools already on your computer, such as Codex and Claude Code, with no new account or API key. Your sign-in stays on your computer.',
  },
  {
    src: 'developers/index.html',
    title: 'Add WebDeviceCable to your website',
    description: 'Let your users run AI requests on the Codex or Claude Code they already have, from your web page or from your server. No provider API keys needed.',
  },
  { src: '404.html', title: 'Page not found · WebDeviceCable', description: 'This page does not exist.', notFound: true },
];

export class BuildError extends Error {}
const fail = (message) => { throw new BuildError(message); };

const escapeHtml = (value) => String(value)
  .replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;')
  .replaceAll('"', '&quot;').replaceAll("'", '&#39;');

const formatSize = (bytes) => `${(bytes / 1e6).toFixed(1)} MB`;
const hash = (content) => createHash('sha256').update(content).digest('hex').slice(0, 10);

// ---------- Release data ----------

export function validateRelease(release, downloadNames) {
  if (!release || typeof release !== 'object') fail('Release data is not an object.');
  const keys = Object.keys(downloadNames).sort();
  const known = Object.keys(TARGETS).sort();
  if (keys.join() !== known.join()) fail(`scripts/download-names.json targets (${keys}) do not match the site's targets (${known}).`);
  if (release.draft) fail(`Release ${release.tag_name} is a draft.`);
  if (release.prerelease) fail(`Release ${release.tag_name} is a pre-release; the site only links stable releases.`);
  const match = /^v(\d+\.\d+\.\d+)$/.exec(release.tag_name ?? '');
  if (!match) fail(`Latest release tag "${release.tag_name}" is not a desktop version tag like v1.2.3.`);
  const tag = release.tag_name;
  const version = match[1];
  const releaseUrl = `${REPO_URL}/releases/tag/${tag}`;
  if (release.html_url !== releaseUrl) fail(`Release page URL ${release.html_url} is not ${releaseUrl}.`);
  const assets = new Map((release.assets ?? []).map((asset) => [asset.name, asset]));
  const asset = (name) => {
    const found = assets.get(name);
    if (!found) fail(`Release ${tag} is missing ${name}.`);
    if (found.state !== 'uploaded' || !(found.size > 0)) fail(`Release asset ${name} is not fully uploaded.`);
    const expected = `${REPO_URL}/releases/download/${tag}/${name}`;
    if (found.browser_download_url !== expected) fail(`Release asset ${name} points to ${found.browser_download_url}, expected ${expected}.`);
    const sha256 = /^sha256:([0-9a-f]{64})$/.exec(found.digest ?? '')?.[1] ?? null;
    return { name, url: expected, size: found.size, sha256 };
  };
  const downloads = {};
  for (const key of Object.keys(TARGETS)) {
    downloads[key] = asset(`WebDeviceCable-${version}-${downloadNames[key]}${TARGETS[key].ext}`);
  }
  return { tag, version, releaseUrl, downloads, checksums: asset('SHA256SUMS') };
}

async function fetchLatestRelease() {
  const token = process.env.GITHUB_TOKEN || process.env.GH_TOKEN;
  const headers = { Accept: 'application/vnd.github+json', 'User-Agent': 'webdevicecable-site-build', 'X-GitHub-Api-Version': '2022-11-28' };
  if (token) headers.Authorization = `Bearer ${token}`;
  let lastError;
  for (let attempt = 1; attempt <= 3; attempt++) {
    try {
      const response = await fetch(`https://api.github.com/repos/${REPO}/releases/latest`, { headers });
      if (!response.ok) throw new Error(`GitHub API answered ${response.status} ${response.statusText}`);
      return await response.json();
    } catch (error) {
      lastError = error;
      if (attempt < 3) await new Promise((resolve) => setTimeout(resolve, attempt * 2000));
    }
  }
  fail(`Could not read the latest release: ${lastError.message}`);
}

// ---------- Inputs from the panel and SDK ----------

export function extractTokens(styleCss, themeCss) {
  const block = /:root\s*\{[^}]*\}/.exec(styleCss)?.[0];
  if (!block || !block.includes('--radius-card')) fail('Could not find the layout tokens block in internal/bridge/web/style.css.');
  const fontStack = /:root\s*\{[^}]*?font-family:\s*([^;]+);/.exec(styleCss)?.[1]?.trim();
  if (!fontStack) fail('Could not find the font stack in internal/bridge/web/style.css.');
  const canvas = /--canvas:\s*(#[0-9a-fA-F]{6})\s*;/.exec(themeCss)?.[1];
  if (!canvas) fail('Could not find --canvas in internal/bridge/web/theme.css.');
  const css = `/* Generated by scripts/site-build.mjs from internal/bridge/web/style.css. Do not edit. */\n${block}\n:root { --font-ui: ${fontStack}; }\n`;
  return { css, fontStack, canvas };
}

export function extractSdkExample(readme) {
  const section = readme.split(/^## Connect from a button\s*$/m)[1];
  const code = section && /```js\n([\s\S]*?)```/.exec(section)?.[1];
  if (!code) fail('Could not find the "Connect from a button" example in sdk/README.md.');
  return code.trimEnd();
}

// Colours JavaScript examples at build time, so the page needs no highlighting script.
// Covers what the examples use: comments, strings, numbers, keywords, class names and calls.
const JS_TOKEN = new RegExp([
  String.raw`(?<comment>\/\/[^\n]*)`,
  String.raw`(?<string>'(?:\\.|[^'\\\n])*'|"(?:\\.|[^"\\\n])*"|\x60(?:\\.|[^\x60\\])*\x60)`,
  String.raw`\b(?<number>\d+(?:\.\d+)?)\b`,
  String.raw`\b(?<keyword>import|export|from|const|let|var|await|async|new|return|if|else|throw|function|for|of|try|catch)\b`,
  // Class names start with a capital and contain a lowercase letter; ALL_CAPS names stay plain.
  String.raw`\b(?<type>[A-Z][\w$]*[a-z][\w$]*)\b`,
  String.raw`\b(?<call>[a-zA-Z_$][\w$]*)(?=\()`,
].join('|'), 'g');

export function highlightJs(code) {
  let out = '';
  let last = 0;
  for (const match of code.matchAll(JS_TOKEN)) {
    const kind = Object.keys(match.groups).find((name) => match.groups[name] !== undefined);
    out += escapeHtml(code.slice(last, match.index)) + `<span class="tok-${kind}">${escapeHtml(match[0])}</span>`;
    last = match.index + match[0].length;
  }
  // Raw values pass through the template engine again, so braces must not read as {{tags}}.
  return (out + escapeHtml(code.slice(last))).replaceAll('{', '&#123;').replaceAll('}', '&#125;');
}

// ---------- Rendering ----------

function renderCta(release, id) {
  const variant = (key, inner) => `<div class="cta-variant" data-for="${key}"${key === 'default' ? '' : ' hidden'}>${inner}</div>`;
  const icon = '<svg class="icon" aria-hidden="true"><use href="{{asset icons.svg}}#download"></use></svg>';
  const parts = [variant('default', `
      <a class="button primary large" href="#downloads">See downloads</a>
      <p class="cta-note">Version ${release.version} · Mac, Windows and Linux · Free and open source</p>`)];
  for (const [key, target] of Object.entries(TARGETS)) {
    const download = release.downloads[key];
    const other = ALT_ARCH[key];
    const alt = other
      ? `<a class="alt-arch" href="${release.downloads[other.key].url}" data-download>${other.text}</a>`
      : '';
    parts.push(variant(key.replace('/', '-'), `
      <a class="button primary large" href="${download.url}" data-download>${icon}Download for ${target.osName}</a>
      <p class="cta-note">Version ${release.version} · ${target.chip} · Free and open source</p>
      <p class="cta-links">${alt}<a href="#downloads">Other downloads</a></p>`));
  }
  parts.push(variant('unsupported', `
      <p class="cta-unsupported">WebDeviceCable runs on Mac, Windows and Linux computers.</p>
      <a class="button secondary large" href="#downloads">See downloads</a>`));
  return `<div class="cta" id="${id}">${parts.join('')}\n    </div>`;
}

function renderDownloads(release) {
  const rows = Object.entries(TARGETS).map(([key, target]) => {
    const download = release.downloads[key];
    return `
        <li class="download-row" data-dl="${key.replace('/', '-')}" data-os-group="${target.os}">
          <a href="${download.url}" data-download>
            <span class="download-name">${escapeHtml(target.plain)}</span>
            <span class="download-file">${escapeHtml(download.name)} · ${formatSize(download.size)}</span>
          </a>
        </li>`;
  }).join('');
  return `<ul class="download-list">${rows}\n      </ul>
      <p class="download-extra">
        <a href="${release.checksums.url}">Checksums (SHA256SUMS)</a>
        <a href="${release.releaseUrl}">Version ${release.version} on GitHub</a>
        <a href="${REPO_URL}/releases">All versions</a>
      </p>`;
}

function renderTemplate(template, context, partials, depth = 0) {
  if (depth > 5) fail('Partials are nested too deeply.');
  let out = template.replace(/\{\{>\s*([\w-]+)\s*\}\}/g, (_, name) => {
    if (!(name in partials)) fail(`Unknown partial "${name}".`);
    return renderTemplate(partials[name], context, partials, depth + 1);
  });
  out = out.replace(/\{\{\{\s*([\w]+)\s*\}\}\}/g, (_, name) => {
    if (!(name in context.raw)) fail(`Unknown raw value "${name}".`);
    return renderTemplate(context.raw[name], context, partials, depth + 1);
  });
  out = out.replace(/\{\{\s*asset\s+([\w.-]+)\s*\}\}/g, (_, file) => {
    if (!(file in context.assetHashes)) fail(`Unknown asset "${file}".`);
    return `${context.root}assets/${file}?v=${context.assetHashes[file]}`;
  });
  out = out.replace(/\{\{\s*([\w]+)\s*\}\}/g, (_, name) => {
    if (!(name in context.values)) fail(`Unknown value "${name}".`);
    return escapeHtml(context.values[name]);
  });
  return out;
}

const pageUrl = (siteUrl, src) => `${siteUrl}/${src.replace(/(^|\/)index\.html$/, '$1')}`;

export function buildSite({ release: releaseData, siteUrl = DEFAULT_SITE_URL, root = repoRoot }) {
  const read = (file) => readFileSync(path.join(root, file));
  const readText = (file) => read(file).toString('utf8');
  let site;
  try { site = new URL(siteUrl); } catch { fail(`--site-url "${siteUrl}" is not a URL.`); }
  if (site.protocol !== 'https:' && !['localhost', '127.0.0.1'].includes(site.hostname)) fail('The site URL must use https.');
  siteUrl = `${site.origin}${site.pathname.replace(/\/+$/, '')}`;
  const basePath = `${site.pathname.replace(/\/+$/, '')}/`;

  const release = validateRelease(releaseData, JSON.parse(readText('scripts/download-names.json')));
  const themeCss = read('internal/bridge/web/theme.css');
  const tokens = extractTokens(readText('internal/bridge/web/style.css'), themeCss.toString('utf8'));

  const files = new Map();
  for (const file of PANEL_FILES) files.set(`assets/${file}`, read(`internal/bridge/web/${file}`));
  for (const file of SITE_ASSETS) files.set(`assets/${file}`, read(`site/${file}`));
  files.set('assets/tokens.css', Buffer.from(tokens.css));
  const assetHashes = Object.fromEntries([...files].map(([name, content]) => [name.slice('assets/'.length), hash(content)]));

  const partialDir = path.join(root, 'site/partials');
  const partials = Object.fromEntries(readdirSync(partialDir).filter((f) => f.endsWith('.html'))
    .map((f) => [f.slice(0, -5), readFileSync(path.join(partialDir, f), 'utf8')]));

  for (const page of PAGES) {
    const depth = page.notFound ? 0 : page.src.split('/').length - 1;
    const root = '../'.repeat(depth);
    const canonical = page.notFound ? '' : pageUrl(siteUrl, page.src);
    const context = {
      root,
      assetHashes,
      values: {
        root: root || './',
        home: root || './',
        developers: `${root}developers/`,
        title: page.title,
        description: page.description,
        canonical,
        og_image: `${siteUrl}/assets/og.png`,
        theme_color: tokens.canvas,
        version: release.version,
        release_url: release.releaseUrl,
        repo_url: REPO_URL,
      },
      raw: {
        base: page.notFound ? `<base href="${escapeHtml(basePath)}">` : '',
        canonical_link: page.notFound ? '<meta name="robots" content="noindex">' : `<link rel="canonical" href="${escapeHtml(canonical)}">\n  <meta property="og:url" content="${escapeHtml(canonical)}">`,
        cta_hero: renderCta(release, 'download'),
        cta_end: renderCta(release, 'download-end'),
        downloads: renderDownloads(release),
        sdk_example: highlightJs(extractSdkExample(readText('sdk/README.md'))),
        relay_example: highlightJs(readText('site/examples/relay.js').trimEnd()),
      },
    };
    files.set(page.src, Buffer.from(renderTemplate(readText(`site/${page.src}`), context, partials)));
  }

  const indexable = PAGES.filter((page) => !page.notFound);
  files.set('sitemap.xml', Buffer.from(`<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n${indexable.map((page) => `  <url><loc>${escapeHtml(pageUrl(siteUrl, page.src))}</loc></url>`).join('\n')}\n</urlset>\n`));
  files.set('robots.txt', Buffer.from(`User-agent: *\nAllow: /\nSitemap: ${siteUrl}/sitemap.xml\n`));

  verifyOutput(files, siteUrl);
  return { files, release };
}

// ---------- Output checks ----------

const ALLOWED_EXTERNAL = [
  `${REPO_URL}/`, `${REPO_URL}`,
  'https://www.npmjs.com/package/@webdevicecable/',
  'https://support.apple.com/', 'https://learn.microsoft.com/',
  'https://developers.openai.com/codex/', 'https://code.claude.com/docs/',
];

export function verifyOutput(files, siteUrl = DEFAULT_SITE_URL) {
  const problems = [];
  const ownSite = `${siteUrl}/`;
  const ids = new Map();
  for (const [name, content] of files) {
    if (!name.endsWith('.html')) continue;
    const html = content.toString('utf8');
    ids.set(name, new Set([...html.matchAll(/\sid="([^"]+)"/g)].map((m) => m[1])));
  }
  for (const [name, content] of files) {
    if (!/\.(html|css)$/.test(name)) continue;
    const text = content.toString('utf8');
    const leftover = /\{\{|\}\}|@[A-Z_]+@/.exec(text);
    if (leftover) problems.push(`${name}: unreplaced placeholder near "${text.slice(leftover.index, leftover.index + 30)}"`);
    const isNotFound = name === '404.html';
    const refs = name.endsWith('.css')
      ? [...text.matchAll(/url\(\s*['"]?([^'")]+)['"]?\s*\)/g)].map((m) => m[1])
      : [...text.matchAll(/\s(?:href|src)="([^"]*)"/g)].map((m) => m[1]);
    for (const ref of refs) {
      if (/^(mailto:|data:)/.test(ref)) continue;
      if (ref.startsWith(ownSite)) continue; // canonical link to this page
      if (/^https?:\/\//.test(ref)) {
        if (ref.startsWith('http://')) problems.push(`${name}: insecure link ${ref}`);
        else if (!ALLOWED_EXTERNAL.some((prefix) => ref === prefix || ref.startsWith(prefix))) problems.push(`${name}: external link not on the allowlist: ${ref}`);
        continue;
      }
      if (ref.startsWith('//')) { problems.push(`${name}: protocol-relative URL ${ref}`); continue; }
      if (ref.startsWith('/') && !(isNotFound && /<base href="\/[^"]*"/.test(text) && text.includes(`<base href="${ref}"`))) {
        problems.push(`${name}: root-absolute URL ${ref} breaks on a project subpath`);
        continue;
      }
      if (ref.startsWith('/')) continue;
      const [pathPart, fragment] = ref.split('#');
      const cleanPath = pathPart.split('?')[0];
      const from = isNotFound ? '' : path.posix.dirname(name);
      let target = cleanPath === '' ? name : path.posix.normalize(path.posix.join(from === '.' ? '' : from, cleanPath));
      if (target.startsWith('..')) { problems.push(`${name}: link leaves the site: ${ref}`); continue; }
      if (target === '.' || target.endsWith('/') || !path.posix.extname(target)) target = path.posix.join(target === '.' ? '' : target, 'index.html');
      if (!files.has(target)) { problems.push(`${name}: broken link ${ref}`); continue; }
      if (fragment && target.endsWith('.html') && !ids.get(target)?.has(fragment)) problems.push(`${name}: missing #${fragment} in ${target}`);
      if (fragment && target.endsWith('.svg') && !files.get(target).toString('utf8').includes(`id="${fragment}"`)) problems.push(`${name}: missing icon #${fragment}`);
    }
    if (name.endsWith('.html')) {
      if (!/<html lang="en"/.test(text)) problems.push(`${name}: missing lang`);
      if (!/<meta name="color-scheme" content="dark">/.test(text)) problems.push(`${name}: missing color-scheme`);
      if (/<script(?![^>]*\ssrc=)[^>]*>/.test(text)) problems.push(`${name}: inline scripts are not allowed`);
      if (/\sstyle="/.test(text)) problems.push(`${name}: inline styles are not allowed`);
    }
  }
  if (problems.length) fail(`Site output failed checks:\n  ${problems.join('\n  ')}`);
}

function writeOutput(outDir, files) {
  const resolved = path.resolve(outDir);
  if (resolved === path.parse(resolved).root || repoRoot.startsWith(resolved)) fail(`Refusing to replace ${resolved}.`);
  if (existsSync(resolved) && readdirSync(resolved).length > 0 && !existsSync(path.join(resolved, 'assets/site.css'))) {
    fail(`${resolved} is not empty and does not look like a previous site build; refusing to replace it.`);
  }
  rmSync(resolved, { recursive: true, force: true });
  for (const [name, content] of files) {
    const file = path.join(resolved, name);
    mkdirSync(path.dirname(file), { recursive: true });
    writeFileSync(file, content);
  }
}

async function main() {
  const { values } = parseArgs({
    options: {
      out: { type: 'string', default: 'dist/site' },
      'release-json': { type: 'string' },
      'site-url': { type: 'string' },
    },
  });
  const release = values['release-json'] ? JSON.parse(readFileSync(values['release-json'], 'utf8')) : await fetchLatestRelease();
  const siteUrl = values['site-url'] || process.env.SITE_URL || DEFAULT_SITE_URL;
  const { files, release: info } = buildSite({ release, siteUrl });
  writeOutput(values.out, files);
  console.log(`Built ${files.size} files for WebDeviceCable ${info.version} into ${values.out} (${siteUrl}/)`);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch((error) => {
    console.error(error instanceof BuildError ? `site-build: ${error.message}` : error);
    process.exit(1);
  });
}
