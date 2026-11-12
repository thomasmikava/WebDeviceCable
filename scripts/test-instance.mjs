// Never discover or reuse an installed runner in a development check.
import {mkdir, mkdtemp, realpath, readdir, readFile} from 'node:fs/promises';
import {isAbsolute, relative, resolve, sep} from 'node:path';

export function testPort(raw = process.env.WDC_E2E_PORT ?? '0') {
  if (!/^\d+$/.test(raw) || Number(raw) < 0 || Number(raw) > 65535 || Number(raw) === 47321) throw new Error('Tests require a port other than the installed WDC port.');
  return String(Number(raw));
}

export async function testDirectory(prefix) {
  // Validate before creating or writing anything.
  testPort();
  const root = resolve('.local');
  await mkdir(root, {recursive:true});
  if (await realpath(root) !== root) throw new Error('.local must not be a symlink.');
  if (!process.env.WDC_E2E_DATA_DIR) return mkdtemp(root + sep + prefix + '-');
  const dir = resolve(process.env.WDC_E2E_DATA_DIR);
  const inside = relative(await realpath(root), dir);
  if (!inside || inside.includes(sep) || inside === '..' || isAbsolute(inside)) throw new Error('WDC_E2E_DATA_DIR must be a disposable direct child of .local/.');
  await mkdir(dir, {recursive:true});
  // Reject symlinks and nonempty directories before overwriting or deleting state.
  if (await realpath(dir) !== dir || (await readdir(dir)).length) throw new Error('WDC_E2E_DATA_DIR must be empty and contain no symlink components.');
  return dir;
}

export async function testRuntime(dir, child) {
  const info = JSON.parse(await readFile(resolve(dir, 'runner.json'), 'utf8'));
  if (info.pid !== child.pid) throw new Error('Waiting for our own runner.');
  const url = new URL(info.url);
  if (url.protocol !== 'http:' || url.hostname !== '127.0.0.1' || !url.port || url.username || url.password || url.pathname !== '/' || url.search || url.hash || url.port === '47321') throw new Error('Refusing an unsafe test runner URL.');
  if (testPort() !== '0' && url.port !== testPort()) throw new Error('Test runner used an unexpected port.');
  return url.origin;
}
