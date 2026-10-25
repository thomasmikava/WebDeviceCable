/** Protocol v2: pinned identities, mutual signatures and ephemeral encrypted transport. */
export interface RunnerIdentity { deviceId: string; publicKey: string; endpoint: string }
export interface ConnectionKeys { pairingId: string; identity: RunnerIdentity; privateKey: CryptoKey; publicKey: string }
export interface Invitation extends RunnerIdentity { v: 2; secret: string; appId: string; appUser: string; origin: string; expiresAt: number }
export type ErrorCategory = 'validation' | 'authentication' | 'permission' | 'unavailable' | 'rate_limit' | 'context_limit' | 'cancelled' | 'timeout' | 'disconnected' | 'execution' | 'protocol' | 'conflict' | 'interrupted' | 'internal' | 'transport' | 'security';
export interface Failure { code: string; category: ErrorCategory; message: string; retryable: boolean; raw?: string }
export class BridgeError extends Error {
  readonly category: ErrorCategory;
  readonly retryable: boolean;
  readonly raw?: string;
  idempotencyKey?: string;
  constructor(public code: string, message: string, public status: number, details: Partial<Failure> = {}) {
    super(message); this.name = 'BridgeError';
    this.category = details.category ?? (code === 'verification_failed' ? 'security' : status === 400 ? 'validation' : status === 401 ? 'authentication' : status === 403 ? 'permission' : status === 409 ? 'conflict' : status === 410 ? 'disconnected' : status === 429 ? 'rate_limit' : 'internal');
    this.retryable = details.retryable ?? false; this.raw = details.raw;
  }
}
export class BridgeSecurityError extends BridgeError {
  constructor(message = 'The local runner could not be authenticated. Reconnect using a trusted pairing invitation.') { super('verification_failed', message, 0); this.name = 'BridgeSecurityError'; }
}
interface Signed { payload: string; signature: string }
interface Encrypted { sessionId: string; iv: string; data: string }
interface Channel { fragments:boolean; requests: number; id: string; expiresAt: number; send: CryptoKey; receive: CryptoKey; verifier: CryptoKey }
interface RequestBinding { v: number; kind: string; sessionId: string; nonce: string; timestamp: number; origin: string; pairingId: string; publicKey: string; method: string; path: string; authorization?: string; lease?: string; body: string }
interface Frame { v: number; kind: 'response' | 'event' | 'end' | 'fragment'; sessionId: string; nonce: string; requestHash: string; origin: string; pairingId: string; method: string; path: string; sequence: number; previous: string; status: number; body: string }
const encode = new TextEncoder();
const decode = new TextDecoder('utf-8', { fatal: true });
export function base64url(bytes: Uint8Array): string {
  let text = ''; for (let i = 0; i < bytes.length; i += 8192) text += String.fromCharCode(...bytes.subarray(i, i + 8192));
  return btoa(text).replaceAll('+', '-').replaceAll('/', '_').replace(/=+$/, '');
}
export function unbase64url(text: string): Uint8Array<ArrayBuffer> {
  if (typeof text !== 'string' || !/^[A-Za-z0-9_-]*$/.test(text) || text.length > 256 * 1024) throw new BridgeSecurityError('Invalid secure message encoding.');
  try { const value = atob(text.replaceAll('-', '+').replaceAll('_', '/')); return Uint8Array.from(value, c => c.charCodeAt(0)); }
  catch { throw new BridgeSecurityError('Invalid secure message encoding.'); }
}
export const nonce = () => Array.from(crypto.getRandomValues(new Uint8Array(32)), v => v.toString(16).padStart(2, '0')).join('');
async function hash(bytes: Uint8Array<ArrayBuffer>): Promise<string> { return Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', bytes)), v => v.toString(16).padStart(2, '0')).join(''); }
export function localURL(raw: string): string {
  const url = new URL(raw);
  if (url.protocol !== 'http:' || url.hostname !== '127.0.0.1' || url.username || url.password || url.search || url.hash || url.pathname !== '/') throw new Error('Bridge URL must be an HTTP origin on 127.0.0.1.');
  return url.origin;
}
export function checkOrigin(origin: string): void {
  if (new URL(origin).origin !== origin || typeof window !== 'undefined' && window.location.origin !== origin) throw new Error('App origin mismatch.');
}
export function parseInvitation(value: string): Invitation {
  try {
    if (!value.startsWith('aib2.') || value.length > 8192) throw new Error();
    const in_ = JSON.parse(decode.decode(unbase64url(value.slice(5)))) as Invitation;
    if (in_.v !== 2 || !/^[a-f0-9]{64}$/.test(in_.secret) || !/^[a-f0-9]{64}$/.test(in_.deviceId) || unbase64url(in_.publicKey).length !== 65 || !Number.isFinite(in_.expiresAt) || typeof in_.appId !== 'string' || typeof in_.appUser !== 'string' || typeof in_.origin !== 'string') throw new Error();
    localURL(in_.endpoint); if (Date.now() >= in_.expiresAt) throw new Error(); return in_;
  } catch { throw new BridgeSecurityError('Use a current invitation copied from your installed WebDeviceCable control panel. Legacy invitations are not accepted.'); }
}
export async function createSigningKeys(): Promise<{ privateKey: CryptoKey; publicKey: string }> {
  const keys = await crypto.subtle.generateKey({ name: 'ECDSA', namedCurve: 'P-256' }, false, ['sign', 'verify']);
  return { privateKey: keys.privateKey, publicKey: base64url(new Uint8Array(await crypto.subtle.exportKey('raw', keys.publicKey))) };
}
async function verify(publicKey: CryptoKey, message: Signed): Promise<{ bytes: Uint8Array<ArrayBuffer>; data: Record<string, unknown> }> {
  try {
    const bytes = unbase64url(message.payload), signature = unbase64url(message.signature);
    if (signature.length !== 64 || !await crypto.subtle.verify({ name: 'ECDSA', hash: 'SHA-256' }, publicKey, signature, bytes)) throw new Error();
    return { bytes, data: JSON.parse(decode.decode(bytes)) };
  } catch { throw new BridgeSecurityError('Runner signature verification failed. No unverified output was accepted.'); }
}
async function derive(shared: ArrayBuffer, salt: Uint8Array<ArrayBuffer>, id: string, direction: string): Promise<CryptoKey> {
  const key = await crypto.subtle.importKey('raw', shared, 'HKDF', false, ['deriveKey']);
  return crypto.subtle.deriveKey({ name: 'HKDF', hash: 'SHA-256', salt, info: encode.encode(`ai-bridge/v2/${id}/${direction}`) }, key, { name: 'AES-GCM', length: 256 }, false, ['encrypt', 'decrypt']);
}
async function boundedJSON<T>(response: Response): Promise<T> {
  if (!response.body) throw new BridgeSecurityError('Secure response body unavailable.');
  const reader = response.body.getReader(), parts: Uint8Array[] = []; let size = 0;
  try {
    while (true) {
      const { value, done } = await reader.read(); if (done) break;
      size += value.length; if (size > 256 * 1024) throw new BridgeSecurityError('Secure response exceeds the size limit.');
      parts.push(value);
    }
    const bytes = new Uint8Array(size); let offset = 0;
    for (const part of parts) { bytes.set(part, offset); offset += part.length; }
    try { return JSON.parse(decode.decode(bytes)) as T; } catch { throw new BridgeSecurityError('Invalid secure response.'); }
  } finally { await reader.cancel().catch(() => {}); reader.releaseLock(); }
}
async function seal(key: CryptoKey, id: string, bytes: Uint8Array<ArrayBuffer>): Promise<Encrypted> {
  const iv = crypto.getRandomValues(new Uint8Array(12));
  const data = await crypto.subtle.encrypt({ name: 'AES-GCM', iv, additionalData: encode.encode(`ai-bridge/v2/${id}/request`), tagLength: 128 }, key, bytes);
  return { sessionId: id, iv: base64url(iv), data: base64url(new Uint8Array(data)) };
}
async function open(key: CryptoKey, id: string, envelope: Encrypted): Promise<Signed> {
  try {
    if (envelope.sessionId !== id) throw new Error(); const iv = unbase64url(envelope.iv); if (iv.length !== 12) throw new Error();
    const bytes = await crypto.subtle.decrypt({ name: 'AES-GCM', iv, additionalData: encode.encode(`ai-bridge/v2/${id}/response`), tagLength: 128 }, key, unbase64url(envelope.data));
    return JSON.parse(decode.decode(bytes));
  } catch { throw new BridgeSecurityError('Encrypted response authentication failed.'); }
}
export class SecureTransport {
  readonly endpoint: string;
  private channel?: Channel;
  private opening?: Promise<Channel>;
  constructor(private identity: RunnerIdentity, private origin: string, private keys: { privateKey: CryptoKey; publicKey: string }, private pairingId: string, endpoint?: string) {
    this.endpoint = localURL(endpoint ?? identity.endpoint);
    if (this.endpoint !== localURL(identity.endpoint)) throw new BridgeSecurityError('Runner address does not match the pinned pairing.');
    checkOrigin(origin);
    if (!keys.privateKey || keys.privateKey.type !== 'private' || keys.privateKey.extractable || keys.privateKey.algorithm.name !== 'ECDSA' || (keys.privateKey.algorithm as EcKeyAlgorithm).namedCurve !== 'P-256' || !keys.privateKey.usages.includes('sign')) throw new BridgeSecurityError('A non-extractable app pairing key is required. Re-pair this app.');
    if (!/^[a-f0-9]{64}$/.test(identity.deviceId) || unbase64url(identity.publicKey).length !== 65 || unbase64url(keys.publicKey).length !== 65) throw new BridgeSecurityError('Invalid pinned identity.');
  }
  private async getChannel(): Promise<Channel> {
    if (this.channel && this.channel.requests < 1024 && this.channel.expiresAt - Date.now() > 45000) { this.channel.requests++; return this.channel; }
    if (!this.opening) {
      const previous = this.channel;
      this.opening = this.handshake().then(channel => {
        this.channel = channel;
        if (previous) void this.fetchOnChannel(previous, '/v2/channel/current', {method:'DELETE'}).catch(() => {});
        return channel;
      }).finally(() => { this.opening = undefined; });
    }
    const channel = await this.opening; channel.requests++; return channel;
  }
  async close(): Promise<void> {
    if (this.opening) await this.opening.catch(() => {});
    const channel = this.channel; this.channel = undefined;
    if (channel) await this.fetchOnChannel(channel, '/v2/channel/current', {method:'DELETE', keepalive:true});
  }
  private async handshake(): Promise<Channel> {
    const verifier = await crypto.subtle.importKey('raw', unbase64url(this.identity.publicKey), { name: 'ECDSA', namedCurve: 'P-256' }, false, ['verify']);
    const ephemeral = await crypto.subtle.generateKey({ name: 'ECDH', namedCurve: 'P-256' }, false, ['deriveBits']);
    const publicKey = base64url(new Uint8Array(await crypto.subtle.exportKey('raw', ephemeral.publicKey))), challenge = nonce();
    const response = await fetch(this.endpoint + '/v2/channel', { method: 'POST', credentials: 'omit', cache: 'no-store', redirect: 'error', signal: AbortSignal.timeout(5000), headers: { Origin: this.origin, 'Content-Type': 'application/json' }, body: JSON.stringify({ nonce: challenge, publicKey, features:['fragments-v1'] }) });
    if (!response.ok) throw new BridgeSecurityError('The secure runner connection is unavailable.');
    const signed = await boundedJSON<Signed>(response);
    const { data: hello } = await verify(verifier, signed);
    if (hello.v !== 2 || hello.kind !== 'ai-bridge-channel' || hello.deviceId !== this.identity.deviceId || hello.publicKey !== this.identity.publicKey || hello.endpoint !== this.endpoint || hello.origin !== this.origin || hello.nonce !== challenge || hello.clientPublicKey !== publicKey || typeof hello.sessionId !== 'string' || !/^[a-f0-9]{64}$/.test(hello.sessionId) || typeof hello.expiresAt !== 'number' || hello.expiresAt <= Date.now() || hello.expiresAt > Date.now() + 16 * 60000 || typeof hello.serverPublicKey !== 'string' || typeof hello.salt !== 'string') throw new BridgeSecurityError('Runner handshake does not match this pairing or challenge.');
    const peer = await crypto.subtle.importKey('raw', unbase64url(hello.serverPublicKey), { name: 'ECDH', namedCurve: 'P-256' }, false, []);
    const shared = await crypto.subtle.deriveBits({ name: 'ECDH', public: peer }, ephemeral.privateKey, 256), salt = unbase64url(hello.salt);
    if (salt.length !== 32) throw new BridgeSecurityError('Invalid channel salt.');
    return { fragments:Array.isArray(hello.features)&&hello.features.includes("fragments-v1"), requests: 0, id: hello.sessionId, expiresAt: hello.expiresAt, verifier, send: await derive(shared, salt, hello.sessionId, 'request'), receive: await derive(shared, salt, hello.sessionId, 'response') };
  }
  async fetch(path: string, init: RequestInit = {}, lease = '', invitation = ''): Promise<Response> {
    return this.fetchOnChannel(await this.getChannel(), path, init, lease, invitation);
  }
  private async fetchOnChannel(channel: Channel, path: string, init: RequestInit = {}, lease = '', invitation = ''): Promise<Response> {
    const binding: RequestBinding = { v: 2, kind: 'ai-bridge-request', sessionId: channel.id, nonce: nonce(), timestamp: Date.now(), origin: this.origin, pairingId: this.pairingId, publicKey: this.keys.publicKey, method: init.method ?? 'GET', path, body: typeof init.body === 'string' ? init.body : '', ...(lease ? { lease } : {}), ...(invitation ? { authorization: invitation } : {}) };
    const payload = encode.encode(JSON.stringify(binding));
    const signature = await crypto.subtle.sign({ name: 'ECDSA', hash: 'SHA-256' }, this.keys.privateKey, payload);
    const encrypted = await seal(channel.send, channel.id, encode.encode(JSON.stringify({ payload: base64url(payload), signature: base64url(new Uint8Array(signature)) })));
    const requestHash = await hash(payload), controller = new AbortController(), abort = () => controller.abort(init.signal?.reason);
    const timeout = setTimeout(() => controller.abort(new DOMException('The local runner response timed out.', 'TimeoutError')), path.endsWith('/events') ? 40000 : 10000);
    const cleanup = () => { clearTimeout(timeout); init.signal?.removeEventListener('abort', abort); };
    init.signal?.addEventListener('abort', abort, { once: true }); if (init.signal?.aborted) controller.abort();
    let raw: Response;
    try { raw = await fetch(this.endpoint + '/v2/request', { method: 'POST', credentials: 'omit', cache: 'no-store', redirect: 'error', signal: controller.signal, keepalive: init.keepalive, headers: { Origin: this.origin, 'Content-Type': 'application/json' }, body: JSON.stringify(encrypted) }); }
    catch (error) { cleanup(); throw error; }
    if (!raw.ok) { cleanup(); controller.abort(); throw new BridgeSecurityError('The secure request was rejected or the channel expired. No unsigned response was accepted.'); }
    let sequence = 0, previous = '';
    const readFrame = async (envelope: Encrypted): Promise<Frame> => {
      const signed = await open(channel.receive, channel.id, envelope), verified = await verify(channel.verifier, signed), frame = verified.data as unknown as Frame;
      if (frame.v !== 2 || frame.sessionId !== channel.id || frame.nonce !== binding.nonce || frame.requestHash !== requestHash || frame.origin !== binding.origin || frame.pairingId !== binding.pairingId || frame.method !== binding.method || frame.path !== binding.path || frame.sequence !== sequence || frame.previous !== previous || !Number.isInteger(frame.status) || frame.status < 200 || frame.status > 599 || typeof frame.body !== 'string' || !['response', 'event', 'end', 'fragment'].includes(frame.kind)) throw new BridgeSecurityError('Response binding, sequence or stream chain verification failed.');
      if(frame.kind==='fragment'&&!channel.fragments)throw new BridgeSecurityError('Unnegotiated stream extension.');
 sequence++; previous = await hash(verified.bytes); return frame;
    };
    if (raw.headers.get('Content-Type')?.startsWith('text/event-stream')) {
      if (!path.endsWith('/events')) {
        try {
          let result: Frame | undefined;
          for await (const frame of this.frames(raw, readFrame, controller, cleanup)) { if (result || frame.kind !== 'response') throw new BridgeSecurityError('Expected one fragmented response.'); result = frame; }
          if (!result) throw new BridgeSecurityError('Incomplete response.');
          return new Response(result.body,{status:result.status,headers:{'Content-Type':'application/json'}});
        } finally { cleanup(); }
      }
      const iterator = this.events(raw, readFrame, controller, cleanup);
      const stream = new ReadableStream<Uint8Array>({
        async pull(output) { try { const part = await iterator.next(); if (part.done) output.close(); else output.enqueue(encode.encode(part.value)); } catch (error) { output.error(error); } },
        async cancel() { controller.abort(); void iterator.return(undefined).catch(()=>{}); },
      });
      return new Response(stream, { status: 200, headers: { 'Content-Type': 'text/event-stream' } });
    }
    try {
      const envelope = await boundedJSON<Encrypted>(raw);
      const frame = await readFrame(envelope); if (frame.kind !== 'response' || sequence !== 1) throw new BridgeSecurityError('Expected one signed response.');
      return new Response(frame.body, { status: frame.status, headers: { 'Content-Type': 'application/json' } });
    } finally { cleanup(); }
  }
  private async *frames(raw: Response, readFrame: (message: Encrypted) => Promise<Frame>, controller: AbortController, cleanup: () => void): AsyncGenerator<Frame> {
    if (!raw.body) throw new BridgeSecurityError('Streaming body unavailable.');
    const reader=raw.body.getReader(), decoder=new TextDecoder('utf-8',{fatal:true});
    let buffer='', ended=false, size=0; let parts: Uint8Array<ArrayBuffer>[]=[]; let status: number | undefined;
    try {
      while(true) {
        const chunk=await reader.read(); if(chunk.done) break;
        buffer+=decoder.decode(chunk.value,{stream:true});
        let boundary: number;
        while((boundary=buffer.indexOf('\n\n'))>=0) {
          const event=buffer.slice(0,boundary); buffer=buffer.slice(boundary+2);
          if(event.length>512*1024) throw new BridgeSecurityError('Secure stream frame exceeds the size limit.');
          const lines=event.split('\n'), data=lines.filter(line=>line.startsWith('data: '));
          if(!lines.includes('event: bridge') || data.length!==1 || ended) throw new BridgeSecurityError('Invalid secure stream.');
          let envelope: Encrypted; try { envelope=JSON.parse(data[0].slice(6)); } catch { throw new BridgeSecurityError('Malformed secure frame.'); }
          const frame=await readFrame(envelope);
          if(status!==undefined && status!==frame.status) throw new BridgeSecurityError('Fragment status changed.');
          if(frame.kind==='fragment') {
            const bytes=unbase64url(frame.body); if(bytes.length<1 || bytes.length>16384) throw new BridgeSecurityError('Invalid fragment.');
            parts.push(bytes); size+=bytes.length; status=frame.status;
            if(size>52*1024*1024) throw new BridgeSecurityError('Response exceeds the size limit.');
            continue;
          }
          if(parts.length) {
            const tail=encode.encode(frame.body); size+=tail.length;
            if(size>52*1024*1024) throw new BridgeSecurityError('Response exceeds the size limit.');
            const bytes=new Uint8Array(size); let offset=0; for(const part of [...parts,tail]) { bytes.set(part,offset); offset+=part.length; }
            try { frame.body=decode.decode(bytes); } catch { throw new BridgeSecurityError('Invalid fragmented UTF-8.'); }
            parts=[]; size=0; status=undefined;
          }
          ended=frame.kind==='end'||frame.kind==='response';
          yield frame;
          if(ended)return;
        }
        if(buffer.length>512*1024) throw new BridgeSecurityError('Secure stream frame exceeds the size limit.');
      }
      if(!ended || parts.length || buffer.length) throw new TypeError('Secure stream interrupted before its authenticated end.');
    } finally { controller.abort(); cleanup(); void reader.cancel().catch(()=>{}); reader.releaseLock(); }
  }
  private async *events(raw: Response, readFrame: (message: Encrypted) => Promise<Frame>, controller: AbortController, cleanup: () => void): AsyncGenerator<string> {
    for await(const frame of this.frames(raw,readFrame,controller,cleanup)) {
      if(frame.status!==200 || frame.kind==='response') throw new BridgeSecurityError('Invalid stream frame.');
      if(frame.body) {
        let job: {revision:number;status:string}; try {job=JSON.parse(frame.body);} catch {throw new BridgeSecurityError('Invalid verified job payload.');}
        const terminal=['completed','cancelled','timed_out','blocked','interrupted','failed'].includes(job.status);
        if(terminal!==(frame.kind==='end')) throw new BridgeSecurityError('Terminal output requires an authenticated end frame.');
        yield `id: ${job.revision}\nevent: job\ndata: ${frame.body}\n\n`;
      }
    }
  }
}

/** Signed launch rendezvous. The invitation itself is encrypted to an ephemeral key. */
export async function launchRequest(endpoint:string, origin:string, keys:{privateKey:CryptoKey;publicKey:string}, details:{requestId:string;appId:string;name:string;appUser:string}, pinned?:RunnerIdentity):Promise<{identity:RunnerIdentity;status:string;invitation:string}> {
  endpoint=localURL(endpoint);checkOrigin(origin);
  try {
  const ephemeral=await crypto.subtle.generateKey({name:'ECDH',namedCurve:'P-256'},false,['deriveBits']);
  const ephemeralKey=base64url(new Uint8Array(await crypto.subtle.exportKey('raw',ephemeral.publicKey)));
  const request={...details,origin,publicKey:keys.publicKey,nonce:nonce(),timestamp:Date.now(),ephemeralKey};
  const bytes=encode.encode(JSON.stringify(request)), signature=await crypto.subtle.sign({name:'ECDSA',hash:'SHA-256'},keys.privateKey,bytes);
  const response=await fetch(endpoint+'/v2/connect',{method:'POST',credentials:'omit',cache:'no-store',redirect:'error',signal:AbortSignal.timeout(5000),headers:{'Content-Type':'application/json',Origin:origin},body:JSON.stringify({payload:base64url(bytes),signature:base64url(new Uint8Array(signature))})});
  if(!response.ok) throw new BridgeError('launch_required','Open WebDeviceCable to continue.',response.status);
  const signed=await boundedJSON<Signed>(response); let preview:Record<string,unknown>;try{preview=JSON.parse(decode.decode(unbase64url(signed.payload)));}catch{throw new BridgeSecurityError('Invalid launch response.');}
  const identity:RunnerIdentity={endpoint,deviceId:String(preview.deviceId),publicKey:String(preview.publicKey)};
  if(pinned&&(pinned.publicKey!==identity.publicKey||pinned.deviceId!==identity.deviceId||pinned.endpoint!==endpoint))throw new BridgeSecurityError('The runner identity changed during approval.');
  const verifier=await crypto.subtle.importKey('raw',unbase64url(identity.publicKey),{name:'ECDSA',namedCurve:'P-256'},false,['verify']);
  const {data}=await verify(verifier,signed);
  if(data.requestId!==request.requestId||data.nonce!==request.nonce||data.origin!==origin||data.endpoint!==endpoint||data.clientPublicKey!==keys.publicKey||data.clientEphemeralKey!==ephemeralKey)throw new BridgeSecurityError('Launch response does not match this website and request.');
  const peer=await crypto.subtle.importKey('raw',unbase64url(String(data.serverPublicKey)),{name:'ECDH',namedCurve:'P-256'},false,[]);
  const shared=await crypto.subtle.deriveBits({name:'ECDH',public:peer},ephemeral.privateKey,256),salt=unbase64url(String(data.salt));if(salt.length!==32)throw new BridgeSecurityError('Invalid launch salt.');
  const key=await derive(shared,salt,details.requestId,'launch'), envelope=data.encrypted as Encrypted;
  if(envelope.sessionId!==details.requestId)throw new BridgeSecurityError('Launch channel mismatch.');
  const plaintext=await crypto.subtle.decrypt({name:'AES-GCM',iv:unbase64url(envelope.iv),additionalData:encode.encode(`ai-bridge/v2/${details.requestId}/launch`),tagLength:128},key,unbase64url(envelope.data));
  const result=JSON.parse(decode.decode(new Uint8Array(plaintext))) as {status:string;invitation:string};
  if(result.invitation){const invitation=parseInvitation(result.invitation);if(invitation.publicKey!==identity.publicKey||invitation.deviceId!==identity.deviceId||invitation.endpoint!==endpoint)throw new BridgeSecurityError('Invitation changed the approved runner identity.');}
  if(!/^[a-f0-9]{64}$/.test(identity.deviceId)||!['pending','approved','rejected'].includes(result.status)||typeof result.invitation!=='string')throw new BridgeSecurityError('Invalid launch approval.');
  return {identity,...result};
  } catch(error) {
    if(error instanceof BridgeError||error instanceof TypeError||error instanceof DOMException&&['NetworkError','TimeoutError','AbortError'].includes(error.name))throw error;
    throw new BridgeSecurityError('The launch invitation could not be authenticated.');
  }
}
