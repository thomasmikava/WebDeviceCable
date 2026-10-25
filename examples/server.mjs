// Development-only integrated app. Serve only these bundled example/SDK files.
import http from 'node:http';
import { readFile } from 'node:fs/promises';
const port = Number(process.env.BRIDGE_EXAMPLE_PORT ?? 47322);
if (!Number.isInteger(port) || port < 1 || port > 65535) throw new Error('Invalid BRIDGE_EXAMPLE_PORT');
const host = `127.0.0.1:${port}`;
const bridgeURL = new URL(process.env.BRIDGE_URL ?? 'http://127.0.0.1:47321');
if (bridgeURL.protocol !== 'http:' || bridgeURL.hostname !== '127.0.0.1' || bridgeURL.username || bridgeURL.password || bridgeURL.search || bridgeURL.hash || bridgeURL.pathname !== '/') throw new Error('BRIDGE_URL must be a loopback HTTP origin');
const files = new Map([
  ['/', [new URL('./app.html', import.meta.url), 'text/html']],
  ['/sdk.js', [new URL('../internal/bridge/web/index.js', import.meta.url), 'text/javascript']],
  ['/storage.js', [new URL('../internal/bridge/web/storage.js', import.meta.url), 'text/javascript']],
  ['/secure.js', [new URL('../internal/bridge/web/secure.js', import.meta.url), 'text/javascript']],
  ['/app.js', [new URL('./app.js', import.meta.url), 'text/javascript']],
]);
http.createServer(async (request, response) => {
  if (request.headers.host !== host || request.method !== 'GET' || !files.has(request.url)) { response.writeHead(404); response.end(); return; }
  const [path, type] = files.get(request.url);
  response.setHeader('Content-Type',type); response.setHeader('Cache-Control','no-store');
  response.setHeader('Content-Security-Policy',`default-src 'none'; script-src 'self'; style-src 'unsafe-inline'; connect-src ${bridgeURL.origin}; base-uri 'none'; frame-ancestors 'none'`);
  try { const content=await readFile(path); response.end(type==='text/html'?content.toString().replace('<title>',`<meta name="bridge-url" content="${bridgeURL.origin}"><title>`):content); } catch { response.writeHead(500); response.end('Build the SDK first.'); }
}).listen(port, '127.0.0.1', () => console.log(`Development example: http://${host}\nPair app ID writer.example, origin http://${host}, app user alice.`));
