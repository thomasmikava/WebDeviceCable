import { BridgeError, SecureTransport, parseInvitation, createSigningKeys, checkOrigin, localURL, nonce, launchRequest } from './secure.js';
import type { ConnectionKeys, Failure } from './secure.js';
export { ConnectionStore } from './storage.js';
export { BridgeError, BridgeSecurityError } from './secure.js';
export type { ConnectionKeys, RunnerIdentity, Failure, ErrorCategory } from './secure.js';
/** Local-only protocol client. Pairing keys belong to one app user and device. */
export type Provider = 'codex' | 'claude';
export type JobStatus = 'awaiting_approval' | 'queued' | 'running' | 'completed' | 'cancelled' | 'timed_out' | 'blocked' | 'interrupted' | 'failed';
export interface JobRequest {
  idempotencyKey: string;
  kind: 'connection-test' | 'text' | 'image-generate' | 'image-edit' | 'command' | 'action';
  provider?: Provider;
  prompt?: string;
  mode?: 'restricted' | 'commands' | 'app-tools' | 'workspace';
  action?: string;
  arguments?: Record<string, string>;
  timeoutSeconds?: number;
  group?: string;
  conversationId?: string;
 outputSchema?: Record<string,unknown>;
 inputs?: string[];
}
export interface Job {
  id: string; pairingId: string; status: JobStatus; code?: string; detail?: string;
  output?: string; actualProvider?: Provider; actualModel?: string; billing: 'none' | 'native_subscription';
  revision: number; request: JobRequest; createdAt: string; finishedAt?: string;
  actionTitle?: string; exitCode?: number; outputTruncated?: boolean;
  execution?: ExecutionMetadata; error?: Failure; turn?: number;
 json?: unknown; artifacts?: Artifact[]; usage?: TokenUsage; inputNames?: string[];
}
export interface Artifact {id:string;name:string;mediaType:string;size:number;sha256:string}
export interface TokenUsage {inputTokens:number;cachedInputTokens:number;outputTokens:number;reasoningOutputTokens:number;totalTokens:number}
export interface UploadedInput extends Artifact {}
export interface AdmissionStatus { available: boolean; kind?: 'native' | 'command'; code?: string }
export interface Pairing {
  id: string; appId: string; appUser: string; name: string; origin: string;
  deviceId: string; automatic: boolean; paused: boolean; revoked: boolean; mode: 'restricted' | 'commands'; actions?: Record<string, string>;
}
export interface Capability {
  id: Provider; name: string; execution: boolean; eligibility: string; reason: string; source: string;
  appTools:boolean;workspace:boolean;
 text: boolean; json: boolean; imageInput: boolean; documentInput: boolean; imageGeneration: boolean; imageEditing: boolean;
}
export interface ActionArgument { name: string; title?: string; type?: string; default?: string; rejectNull?: boolean; choices?: { title?: string; value: string }[] }
export interface CommandAction { id: string; title?: string; arguments: ActionArgument[] | null; timeout: number; available: boolean; sessions: boolean }
export interface Capabilities {appToolPermissions?:'application';fileTransfer:boolean;maxTextBytes:number; protocolVersion: 2; pairing: Pairing; providers: Capability[]; actions: CommandAction[]; connectionTest: boolean; leaseSeconds: number; modes: string[]; remoteAccess: false }
const done = (job: Job) => ['completed', 'cancelled', 'timed_out', 'blocked', 'interrupted', 'failed'].includes(job.status);
const sleep = (ms: number) => new Promise<void>(resolve => setTimeout(resolve, ms));
async function json<T>(response: Response): Promise<T> {
  let body;
  try { body = await response.json(); }
  catch (error) {
    if (networkError(error)) throw error;
    throw new BridgeError('invalid_response', 'WDC returned an unreadable response. Check the request status before retrying.', response.status, {category:'transport'});
  }
  if (!response.ok) throw new BridgeError(body.error?.code ?? 'http_error', body.error?.message ?? 'Bridge request failed.', response.status, body.error);
  return body as T;
}
export interface ExecutionMetadata { kind: 'command' | 'native' | 'connection-test'; name?: string; version?: string; versionSource?: 'configured' | 'native'; provider?: Provider; model?: string; reasoning?: string }
export interface ActionGroup { id: string; name: string; kind?: 'ai' | 'command'; parentId?: string }
export interface GroupDefinition extends ActionGroup { children?: GroupDefinition[] }
export type ActionGroups = GroupDefinition[] | Record<string, string>;
export interface Conversation { id: string; group?: string; status: 'open' | 'closed'; code?: string; lastJobId?: string; turns: number; createdAt: string; updatedAt: string }
export interface SessionOptions { mode?: "restricted" | "app-tools" | "workspace"; group?: string; provider?: Provider; action?: string; id?: string }
export interface MessageOptions { outputSchema?: Record<string,unknown>; inputs?:string[]; prompt?: string; arguments?: Record<string, string>; timeoutSeconds?: number; idempotencyKey?: string }
export type RunOptions = Omit<JobRequest, 'idempotencyKey' | 'kind'> & { kind?: JobRequest['kind']; idempotencyKey?: string };
export interface AppToolDefinition {id:string;title:string;description:string;inputSchema:Record<string,unknown>;readOnly?:boolean}
export interface AppTool extends AppToolDefinition {execute:(args:Record<string,unknown>,context:{idempotencyKey:string;signal:AbortSignal})=>unknown|Promise<unknown>}
/** App tool metadata is descriptive; its app owns authorization. Legacy grants is always empty. */
export interface PermissionStatus {tools:Record<string,AppToolDefinition&{hash:string}>;grants:Record<string,{hash:string;automatic:boolean}>;folders:{id:string;name:string;write:boolean;automatic:boolean}[]}
export interface ClientOptions { tools?: AppTool[]; baseURL?: string; connection: ConnectionKeys; origin?: string; groups?: ActionGroups; account?: {appId: string; appUser: string} }
export interface BridgeTransport {
  reset(): void;
  fetch(path: string, init?: RequestInit, lease?: string, invitation?: string): Promise<Response>;
  close(): Promise<void>;
}
export class BridgeJobError extends BridgeError {
  constructor(public job: Job) { super(job.error?.code ?? job.code ?? job.status, job.error?.message ?? job.detail ?? 'The request did not complete.', 0, job.error); this.name = 'BridgeJobError'; }
}
function appOrigin(value?: string): string { if (value) return value; if (typeof window !== 'undefined') return window.location.origin; throw new BridgeError('origin_required', 'Supply the app origin outside a browser.', 400); }
function groupsArray(groups: ActionGroups): ActionGroup[] {
  if (!Array.isArray(groups)) return Object.entries(groups).map(([id,name]) => ({id,name}));
  const result: ActionGroup[] = [];
  const visit = (definitions: GroupDefinition[], parent?: string, depth = 1) => {
    if (depth > 8) throw new BridgeError('invalid_groups','Group hierarchies support up to 8 levels.',400);
    for (const definition of definitions) {
      if (result.length >= 64) throw new BridgeError('invalid_groups','An app can declare up to 64 groups.',400);
      const id = parent ? `${parent}.${definition.id}` : definition.id;
      result.push({id,name:definition.name,...(definition.kind ? {kind:definition.kind} : {}),...(parent || definition.parentId ? {parentId:parent ?? definition.parentId} : {})});
      if (definition.children?.length) visit(definition.children,id,depth + 1);
    }
  };
  visit(groups); return result;
}
function networkError(error: unknown): boolean { return error instanceof TypeError || error instanceof DOMException && ['NetworkError', 'TimeoutError'].includes(error.name) || error instanceof BridgeError && error.category === 'transport'; }
function unknownSubmission(error: unknown, key: string): never {
  if (networkError(error)) { const failure = new BridgeError('submission_unknown', 'The submission result is unknown. Retry the same request with the same idempotency key; do not create a new request.', 0, {category:'transport', retryable:true}); failure.idempotencyKey = key; throw failure; }
  if (error instanceof BridgeError && error.status === 0) error.idempotencyKey = key;
  throw error;
}
export class BridgeClient {
  private transport: BridgeTransport;
 private toolHandlers=new Map<string,AppTool>();
 private toolsBusy=false;
 private toolsTimer?:ReturnType<typeof setInterval>;
 private lifetime=new AbortController();
  private generation = 0;
  private lease = '';
  private leaseExpiresAt = 0;
  private connecting?: Promise<void>;
  private timer?: ReturnType<typeof setInterval>;
  private heartbeatPending = false;
  private closed = true;
  private watchers = new Set<AbortController>();
  private onPageHide = () => { void this.close().catch(() => {}); };
  constructor(private options: ClientOptions, transport?: BridgeTransport) {
    const keys = options.connection;
    if (!keys || !/^[a-f0-9]{64}$/.test(keys.pairingId)) throw new Error('A signed protocol v2 pairing is required. Re-pair this app.');
    this.transport = transport ?? new SecureTransport(keys.identity, appOrigin(options.origin), keys, keys.pairingId, options.baseURL);
  }
  static async pair(options: { baseURL?: string; invitation: string; appId: string; appUser: string; origin?: string }): Promise<{ connection: ConnectionKeys; pairing: Pairing }> {
    return BridgeClient.pairWithKeys(options,await createSigningKeys());
 }
 private static async pairWithKeys(options:{baseURL?:string;invitation:string;appId:string;appUser:string;origin?:string}, keys:{privateKey:CryptoKey;publicKey:string}):Promise<{connection:ConnectionKeys;pairing:Pairing}> {
 const origin = appOrigin(options.origin); checkOrigin(origin);
    const invite = parseInvitation(options.invitation);
    if (invite.appId !== options.appId || invite.appUser !== options.appUser || invite.origin !== origin) throw new BridgeError('identity_mismatch', 'Invitation does not match this app, account or origin.', 403);
    const identity = { deviceId: invite.deviceId, publicKey: invite.publicKey, endpoint: invite.endpoint };
    const transport = new SecureTransport(identity, origin, keys, '', options.baseURL);
    try {
    const result = await json<{ pairing: Pairing }>(await transport.fetch('/v2/pairings/claim', { method: 'POST', body: JSON.stringify({ appId: options.appId, appUser: options.appUser }) }, '', invite.secret));
    if (result.pairing.appId !== options.appId || result.pairing.appUser !== options.appUser || result.pairing.origin !== origin || result.pairing.deviceId !== identity.deviceId || !/^[a-f0-9]{64}$/.test(result.pairing.id)) throw new BridgeError('identity_mismatch', 'Verified pairing identity mismatch.', 403);
    return { pairing: result.pairing, connection: { ...keys, identity, pairingId: result.pairing.id } };
    } finally { await transport.close().catch(() => {}); }
  }
  /** Call directly from a click handler. Approval happens in the authenticated local panel. */
  static async requestPairing(options: {appId: string; name: string; appUser: string; baseURL?: string; timeoutMs?: number;launchInstalled?:boolean}): Promise<{connection: ConnectionKeys; pairing: Pairing}> {
    if(options.launchInstalled!==false && typeof document!=='undefined') return BridgeClient.launchPairing(options);
    if (typeof window === 'undefined') throw new BridgeError('browser_required','Interactive pairing requires a browser.',400);
    const endpoint = localURL(options.baseURL ?? 'http://127.0.0.1:47321'), requestId = nonce();
    if (!options.appId || options.appId.length > 100 || !options.name || options.name.length > 100 || !options.appUser || options.appUser.length > 200) throw new BridgeError('invalid_pairing','App ID, name and account are required.',400);
    const timeout = options.timeoutMs ?? 300000;
    if (!Number.isFinite(timeout) || timeout < 1000 || timeout > 300000) throw new BridgeError('invalid_pairing','Pairing timeout must be 1–300 seconds.',400);
    const popup = window.open(`${endpoint}/#connect-request=${requestId}`,'_blank','popup,width=520,height=640');
    if (!popup) throw new BridgeError('popup_blocked','Allow the WebDeviceCable connection window, then try again.',0,{category:'permission'});
    return new Promise((resolve,reject) => {
      let settling = false;
      const cleanup = () => {clearTimeout(deadline);clearInterval(closed);window.removeEventListener('message',receive);};
      const fail = (error: BridgeError) => {if(settling)return;settling = true;cleanup();popup.close();reject(error);};
      const receive = (event: MessageEvent) => {
        if (event.source !== popup || event.origin !== endpoint || event.data?.requestId !== requestId) return;
        if (event.data.type === 'wdc:pairing-ready') popup.postMessage({type:'wdc:pairing-request',requestId,appId:options.appId,name:options.name,appUser:options.appUser},endpoint);
        if (event.data.type === 'wdc:pairing-rejected') fail(new BridgeError('pairing_declined','Connection declined in WebDeviceCable.',403,{category:'permission'}));
        if (event.data.type === 'wdc:pairing-approved' && typeof event.data.invitation === 'string' && !settling) {
          settling = true;cleanup();
          BridgeClient.pair({invitation:event.data.invitation,appId:options.appId,appUser:options.appUser,origin:window.location.origin,baseURL:endpoint}).then(result => {
            popup.postMessage({type:'wdc:pairing-connected',requestId},endpoint);resolve(result);
          },error => {popup.postMessage({type:'wdc:pairing-failed',requestId},endpoint);reject(error);});
        }
      };
      const deadline = setTimeout(() => fail(new BridgeError('pairing_timeout','The connection request expired. Try again.',408,{category:'timeout'})),timeout);
      const closed = setInterval(() => {if(popup.closed)fail(new BridgeError('pairing_declined','The connection window was closed.',403,{category:'permission'}));},300);
      window.addEventListener('message',receive);
    });
  }
  private static async launchPairing(options:{appId:string;name:string;appUser:string;baseURL?:string;timeoutMs?:number}):Promise<{connection:ConnectionKeys;pairing:Pairing}> {
    const endpoint=localURL(options.baseURL??'http://127.0.0.1:47321'), origin=window.location.origin, requestId=nonce();checkOrigin(origin);
    if(!options.appId||options.appId.length>100||!options.name||options.name.length>100||!options.appUser||options.appUser.length>200)throw new BridgeError('invalid_pairing','App ID, name and account are required.',400);
    const timeout=options.timeoutMs??300000;if(!Number.isFinite(timeout)||timeout<1000||timeout>300000)throw new BridgeError('invalid_pairing','Pairing timeout must be 1–300 seconds.',400);
    const popup=window.open('about:blank','_blank','popup,width=460,height=420');if(!popup)throw new BridgeError('popup_blocked','Allow the connection window, then try again.',0,{category:'permission'});
    const doc=popup.document;doc.title='Connect to WebDeviceCable';doc.documentElement.style.colorScheme='dark';doc.body.style.cssText='font:16px system-ui;margin:48px;line-height:1.5';
    const heading=doc.createElement('h1');heading.textContent='Connect this device';heading.style.fontSize='24px';
    const status=doc.createElement('p');status.textContent='Open WebDeviceCable and approve this connection.';
    const link=doc.createElement('a');link.textContent='Open WebDeviceCable';link.style.cssText='display:inline-block;padding:12px 18px;border:1px solid currentColor;border-radius:8px;color:inherit;text-decoration:none';
    const cancel=doc.createElement('button');cancel.textContent='Cancel';cancel.style.cssText='display:block;margin-top:28px';cancel.onclick=()=>popup.close();doc.body.append(heading,status,link,cancel);
    const keys=await createSigningKeys();link.href='webdevicecable://connect?'+new URLSearchParams({requestId,origin,publicKey:keys.publicKey});link.click();
    let pinned: import('./secure.js').RunnerIdentity|undefined;const deadline=Date.now()+timeout;
    try {
      while(Date.now()<deadline&&!popup.closed){
        try {
          const result=await launchRequest(endpoint,origin,keys,{requestId,appId:options.appId,name:options.name,appUser:options.appUser},pinned);pinned=result.identity;
          status.textContent='Waiting for your approval in WebDeviceCable…';
          if(result.status==='rejected')throw new BridgeError('pairing_declined','Connection declined in WebDeviceCable.',403,{category:'permission'});
          if(result.status==='approved')return await BridgeClient.pairWithKeys({invitation:result.invitation,appId:options.appId,appUser:options.appUser,origin,baseURL:endpoint},keys);
        }catch(error){if(error instanceof BridgeError&&error.code!=='launch_required'||!(error instanceof BridgeError)&&!networkError(error))throw error;}
        await sleep(650);
      }
      throw new BridgeError(popup.closed?'pairing_declined':'pairing_timeout',popup.closed?'The connection window was closed.':'The connection request expired. Try again.',408);
    } finally {popup.close();}
  }
  private async request(path: string, init: RequestInit = {}, lease = this.lease): Promise<Response> {
    try { return await this.transport.fetch(path, init, lease); }
    catch (error) { this.transport.reset(); if (networkError(error)) throw new BridgeError('connection_failed', 'Cannot reach the local runner. Check that WebDeviceCable is running.', 0, {category:'transport',retryable:true}); throw error; }
  }
  get connected(): boolean { return !this.closed && Date.now() < this.leaseExpiresAt; }
  async registerTools(tools:AppTool[]):Promise<void> {
    const definitions=tools.map(({execute,...definition})=>definition);
    await this.request('/v2/tools',{method:'POST',body:JSON.stringify({tools:definitions})}).then(json);
    this.toolHandlers=new Map(tools.map(tool=>[tool.id,tool]));
  }
  permissions():Promise<PermissionStatus>{return this.request('/v2/permissions').then(json<PermissionStatus>);}
  permissionRequest(id:string):Promise<{id:string;status:string}>{return this.request(`/v2/permissions/${encodeURIComponent(id)}`).then(json<{id:string;status:string}>);}
  async requestPermissions(request:{tools?:string[];workspace?:boolean},options:{wait?:boolean;signal?:AbortSignal}={}):Promise<{id:string;status:string}>{
    await this.connect();let result=await this.request('/v2/permissions',{method:'POST',body:JSON.stringify(request)}).then(json<{id:string;status:string}>);
    if(options.wait===false)return result;
    while(result.status==='pending'&&!this.closed){if(options.signal?.aborted)throw options.signal.reason??new DOMException('Aborted','AbortError');await sleep(650);result=await this.permissionRequest(result.id);}
    if(result.status!=='completed')throw new BridgeError('permission_denied','The requested access was not approved in WebDeviceCable.',403,{category:'permission'});
    return result;
  }
  private async drainTools():Promise<void>{
    if(this.closed||this.toolsBusy||!this.toolHandlers.size)return;this.toolsBusy=true;
    try {
      const {calls}=await this.request('/v2/tool-calls').then(json<{calls:{id:string;name:string;arguments:Record<string,unknown>}[]}>);
      for(const call of calls){const tool=this.toolHandlers.get(call.name);if(!tool||this.closed)continue;
        // Claim once before invoking. An ambiguous claim is never retried or executed.
        try{await this.request(`/v2/tool-calls/${call.id}/claim`,{method:'POST'}).then(json);}catch{continue;}
        let result:unknown;try{result=await tool.execute(call.arguments,{idempotencyKey:call.id,signal:this.lifetime.signal});if(result===undefined)result=null;}catch{result={error:'The application could not complete this tool call.'};}
        try {if(new TextEncoder().encode(JSON.stringify(result)).length>32768)result={error:'Tool result exceeds the 32 KiB limit. Return a summary or file reference.'};} catch {result={error:'Tool result must be serializable JSON.'};}
 if(!this.closed)await this.request(`/v2/tool-calls/${call.id}/result`,{method:'POST',body:JSON.stringify({result})}).then(json).catch(()=>{});
      }
    }catch{/* The lease and native turn determine cancellation; tool callbacks are never replayed. */}finally{this.toolsBusy=false;}
  }
  registerGroups(groups: ActionGroups): Promise<{groups: ActionGroup[]}> { return this.request('/v2/groups',{method:'POST',body:JSON.stringify({groups:groupsArray(groups)})}).then(json<{groups: ActionGroup[]}>); }
  groups(): Promise<{groups: ActionGroup[]}> { return this.request('/v2/groups').then(json<{groups: ActionGroup[]}>); }
  session(options: SessionOptions = {}): BridgeSession { return new BridgeSession(this, options); }
  async run(input: RunOptions | string, signal?: AbortSignal): Promise<Job> {
    if (signal?.aborted) throw signal.reason ?? new DOMException('Aborted', 'AbortError');
    await this.connect(); const request = typeof input === 'string' ? {prompt:input} : input;
    const job = await this.submit({...request, kind:request.kind ?? (request.action ? 'command' : request.provider ? 'text' : 'action'),idempotencyKey:request.idempotencyKey ?? crypto.randomUUID()});
    let result = job; for await (const update of this.stream(job.id,signal)) result = update;
    if (result.status !== 'completed') throw new BridgeJobError(result); return result;
  }
  async openSession(options: SessionOptions): Promise<Conversation> { await this.connect(); const {id: _id, ...selection} = options; return this.request('/v2/conversations',{method:'POST',body:JSON.stringify(selection)}).then(json<Conversation>); }
  async sendSession(id: string, input: MessageOptions): Promise<Job> {
    const request = {...input,idempotencyKey:input.idempotencyKey ?? crypto.randomUUID()};
    const body = JSON.stringify(request);
    try { return await this.request(`/v2/conversations/${encodeURIComponent(id)}/turns`,{method:'POST',body}).then(json<Job>); }
    catch (error) { return unknownSubmission(error, request.idempotencyKey); }
  }
  getSession(id: string): Promise<Conversation> { return this.request(`/v2/conversations/${encodeURIComponent(id)}`).then(json<Conversation>); }
  closeSession(id: string): Promise<Conversation> { return this.request(`/v2/conversations/${encodeURIComponent(id)}`,{method:'DELETE'}).then(json<Conversation>); }
  capabilities(): Promise<Capabilities> { return this.request('/v2/capabilities').then(json<Capabilities>); }
  async preflight(request: Omit<JobRequest,'idempotencyKey'>, features: string[] = []):Promise<AdmissionStatus>{
    await this.connect();return this.request('/v2/admission',{method:'POST',body:JSON.stringify({request:{...request,idempotencyKey:'preflight-check'},features})}).then(json<AdmissionStatus>);
  }
  /** File bytes stay unchanged and scoped to this requesting tab. */
  async upload(file: Blob, name = 'input'): Promise<UploadedInput> {
    await this.connect(); if(file.size<0||file.size>8*1024*1024)throw new BridgeError('invalid_input','Files must be at most 8 MiB.',400);
    const data=new Uint8Array(await file.arrayBuffer()), sha256=Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256',data))).map(b=>b.toString(16).padStart(2,'0')).join('');
    const input=await this.request('/v2/inputs',{method:'POST',body:JSON.stringify({name,mediaType:file.type,size:data.length,sha256})}).then(json<UploadedInput>);
    try {
      for(let offset=0;offset<data.length;offset+=24576){const part=data.subarray(offset,offset+24576);let binary='';for(const byte of part)binary+=String.fromCharCode(byte);await this.request(`/v2/inputs/${input.id}`,{method:'PUT',body:JSON.stringify({offset,data:btoa(binary)})}).then(json);}
      return input;
    }catch(error){await this.removeInput(input.id).catch(()=>{});throw error;}
  }
  removeInput(id:string):Promise<{ok:boolean}>{return this.request(`/v2/inputs/${encodeURIComponent(id)}`,{method:'DELETE'}).then(json<{ok:boolean}>);}
  /** Signed/encrypted chunks, checked again against the signed file hash. */
  async artifact(job: Job, artifact: Artifact = job.artifacts?.[0] as Artifact):Promise<Blob>{
    if(!artifact||!Number.isSafeInteger(artifact.size)||artifact.size<0||artifact.size>8*1024*1024)throw new BridgeError('invalid_artifact','Invalid output file.',400);
    const data=new Uint8Array(artifact.size);let offset=0;
    while(offset<data.length){const part=await this.request(`/v2/jobs/${encodeURIComponent(job.id)}/artifacts/${encodeURIComponent(artifact.id)}/${offset}`).then(json<{id:string;offset:number;data:string;complete:boolean}>);const bytes=Uint8Array.from(atob(part.data),c=>c.charCodeAt(0));if(part.id!==artifact.id||part.offset!==offset||bytes.length<1||bytes.length>24576||offset+bytes.length>data.length||part.complete!==(offset+bytes.length===data.length))throw new BridgeError('verification_failed','Invalid output file chunk.',0);data.set(bytes,offset);offset+=bytes.length;}
    const hash=Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256',data))).map(b=>b.toString(16).padStart(2,'0')).join('');if(hash!==artifact.sha256)throw new BridgeError('verification_failed','Output file hash mismatch.',0);
    return new Blob([data],{type:artifact.mediaType});
  }
  actions(): Promise<{ actions: CommandAction[] }> { return this.request('/v2/actions').then(json<{ actions: CommandAction[] }>); }
  /** One instance/lease per requesting tab. Do not share a lease between tabs. */
  async connect(): Promise<void> {
    if (this.connecting) return this.connecting;
    if (this.connected) return;
    this.transport.reset();
    if (!this.closed) this.stop();
    const generation = ++this.generation;
    this.connecting = (async () => { try {
      if (this.options.account) {
        const {pairing} = await this.capabilities();
        if (pairing.appId !== this.options.account.appId || pairing.appUser !== this.options.account.appUser || pairing.origin !== appOrigin(this.options.origin) || pairing.deviceId !== this.options.connection.identity.deviceId) throw new BridgeError('identity_mismatch','This connection belongs to another app or account. Disconnect and pair this account.',403);
      }
      await this.openLease(generation); if (this.options.groups) await this.registerGroups(this.options.groups); if(this.options.tools)await this.registerTools(this.options.tools); } catch (error) { await this.close().catch(() => {}); throw error; } })();
    try { await this.connecting; } finally { this.connecting = undefined; }
  }
  private async openLease(generation: number): Promise<void> {
    const result = await json<{ lease: string; expiresAt?: string }>(await this.request('/v2/lease', { method: 'POST' }));
    if (generation !== this.generation) { await this.request('/v2/lease',{method:'DELETE',keepalive:true},result.lease).catch(() => {}); throw new BridgeError('lease_closed','The connection was closed before it finished opening.',410); }
    this.lease = result.lease; this.closed = false; this.lifetime=new AbortController();
    this.leaseExpiresAt = result.expiresAt ? Date.parse(result.expiresAt) : Date.now() + 12000;
    this.timer = setInterval(() => { void this.heartbeat(); }, 3000);
 this.toolsTimer=setInterval(()=>{void this.drainTools();},1000);
    if (typeof window !== 'undefined') window.addEventListener('pagehide', this.onPageHide);
  }
  private async heartbeat(): Promise<void> {
    if (this.closed || this.heartbeatPending) return;
    if (Date.now() >= this.leaseExpiresAt) { this.stop(); return; }
    this.heartbeatPending = true; const generation = this.generation;
    try {
      const response = await this.request('/v2/lease', { method: 'PUT', signal: AbortSignal.timeout(5000) });
      if (generation !== this.generation || this.closed) return;
      if (!response.ok) { this.stop(); }
      else {
        const result = await json<{ expiresAt: string }>(response);
        this.leaseExpiresAt = Date.parse(result.expiresAt);
      }
    } catch { if (generation === this.generation && !this.closed && Date.now() >= this.leaseExpiresAt) this.stop(); }
    finally { this.heartbeatPending = false; }
  }
  private stop(): void {
    clearInterval(this.timer);clearInterval(this.toolsTimer); this.closed = true; this.lifetime.abort();
    if (typeof window !== 'undefined') window.removeEventListener('pagehide', this.onPageHide);
    for (const controller of this.watchers) controller.abort();
    this.watchers.clear();
  }
  async close(): Promise<void> {
    this.generation++;
    const lease = this.lease; this.lease = ''; this.stop();
    try { if (lease) await json(await this.request('/v2/lease', { method: 'DELETE', keepalive: true },lease)); }
    finally { await this.transport.close(); }
  }
  async submit(request: JobRequest): Promise<Job> {
    if (this.closed) throw new BridgeError('lease_required', 'Call connect() for this requesting tab first.', 410);
    const body = JSON.stringify(request);
    try { return await json(await this.request('/v2/jobs', { method: 'POST', body })); } catch (error) { return unknownSubmission(error, request.idempotencyKey); }
  }
  async lookup(idempotencyKey: string): Promise<Job | null> { return (await this.request('/v2/jobs/lookup',{method:'POST',body:JSON.stringify({idempotencyKey})}).then(json<{job: Job | null}>)).job; }
  get(id: string): Promise<Job> { return this.request(`/v2/jobs/${encodeURIComponent(id)}`).then(json<Job>); }
  cancel(id: string): Promise<Job> { return this.request(`/v2/jobs/${encodeURIComponent(id)}/cancel`, { method: 'POST' }).then(json<Job>); }
  /** Full job snapshots, reconnectable without replaying inference or app actions. */
  async *stream(id: string, signal?: AbortSignal): AsyncGenerator<Job> {
    const controller = new AbortController(); this.watchers.add(controller);
    const abort = () => controller.abort(); signal?.addEventListener('abort', abort, { once: true });
    if (signal?.aborted) controller.abort();
    let revision = 0;
    try {
      while (!this.closed && !controller.signal.aborted) {
        let response: Response;
        try { response = await this.request(`/v2/jobs/${encodeURIComponent(id)}/events`, { signal: controller.signal }); }
        catch (error) {
          if (this.closed || controller.signal.aborted || !networkError(error)) throw error;
          await sleep(750); continue;
        }
        if (!response.ok) { await json(response); return; }
        if (!response.body) throw new Error('Streaming body unavailable.');
        const reader = response.body.getReader(); const decoder = new TextDecoder(); let buffer = '';
        try {
          while (true) {
            const chunk = await reader.read(); if (chunk.done) break;
            buffer += decoder.decode(chunk.value, { stream: true });
            if (buffer.length > 52 * 1024 * 1024) throw new Error('Bridge event exceeds size limit.');
            let boundary: number;
            while ((boundary = buffer.indexOf('\n\n')) >= 0) {
              const event = buffer.slice(0, boundary); buffer = buffer.slice(boundary + 2);
              const data = event.split('\n').find(line => line.startsWith('data: ')); if (!data) continue;
              const job = JSON.parse(data.slice(6)) as Job;
              if (job.revision > revision) { revision = job.revision; yield job; }
              if (done(job)) return;
            }
          }
        } catch (error) {
          if (this.closed || controller.signal.aborted) throw error;
          if (!networkError(error)) throw error;
        } finally { await reader.cancel().catch(() => {}); reader.releaseLock(); }
        await sleep(250);
      }
      throw new BridgeError('lease_closed', 'This requesting tab no longer has an active lease.', 410);
    } finally { this.watchers.delete(controller); signal?.removeEventListener('abort', abort); controller.abort(); }
  }
}

/** One server process/context, scoped to this client tab. Never silently restarted. */
export class BridgeSession {
  private conversationId?: string;
  private opening?: Promise<void>;
  private pendingJob?: string;
  private closed = false;
  private submitting = false;
  private uncertainKey?: string;
  constructor(private client: BridgeClient, private options: SessionOptions) {
    if (options.id && (options.group || options.provider || options.action)) throw new BridgeError('invalid_session', 'A resumed session takes an id without a new target selection.',400);
    this.conversationId = options.id;
  }
  get id(): string | undefined { return this.conversationId; }
  async send(input: MessageOptions | string): Promise<Job> {
    if (this.closed) throw new BridgeError('session_closed', 'This session has ended. Create a new session explicitly.',410);
    if (this.submitting) throw new BridgeError('session_busy', 'Wait for the current message to finish.',409);
    const message = typeof input === 'string' ? {prompt:input} : input;
    if (this.uncertainKey && message.idempotencyKey !== this.uncertainKey) { const error = new BridgeError('submission_unknown','Resolve the previous submission with recover(), or retry its exact input and idempotency key.',0,{category:'transport'}); error.idempotencyKey = this.uncertainKey; throw error; }
    this.submitting = true;
    try {
      if (this.conversationId && !this.client.connected) throw new BridgeError('session_closed','This tab connection ended. Create a new session explicitly.',410);
      if (!this.conversationId) {
        if (!this.opening) this.opening = this.client.openSession(this.options).then(conversation => { this.conversationId = conversation.id; });
        try { await this.opening; } finally { this.opening = undefined; }
      }
      if (this.closed) throw new BridgeError('session_closed','This session was closed while it was opening.',410);
      if (this.pendingJob) { const previous = await this.client.get(this.pendingJob); if (!done(previous)) throw new BridgeError('session_busy','Wait for the current message to finish.',409); this.pendingJob = undefined; }
      const job = await this.client.sendSession(this.conversationId!,message); this.pendingJob = job.id; this.uncertainKey = undefined; return job;
    } catch (error) { if (error instanceof BridgeError && error.idempotencyKey) this.uncertainKey = error.idempotencyKey; throw error; } finally { this.submitting = false; }
  }
  async *stream(input: MessageOptions | string, signal?: AbortSignal): AsyncGenerator<Job> {
    if (signal?.aborted) throw signal.reason ?? new DOMException('Aborted','AbortError');
    const job = await this.send(input);
    for await (const update of this.client.stream(job.id,signal)) { if (done(update)) this.pendingJob = undefined; yield update; }
  }
  async run(input: MessageOptions | string, signal?: AbortSignal): Promise<Job> {
    let result: Job | undefined; for await (const update of this.stream(input,signal)) result = update;
    if (!result || result.status !== 'completed') { if (result) throw new BridgeJobError(result); throw new BridgeError('session_closed','The session ended without a result.',410); } return result;
  }
  async recover(): Promise<Job | null> { if (!this.uncertainKey) return this.pendingJob ? this.client.get(this.pendingJob) : null; const job = await this.client.lookup(this.uncertainKey); if (job) {this.pendingJob = done(job) ? undefined : job.id; this.uncertainKey = undefined;} return job; }
  async close(): Promise<void> {
    if (this.closed) return; this.closed = true;
    if (this.opening) await this.opening.catch(() => {});
    if (this.conversationId && this.client.connected) await this.client.closeSession(this.conversationId);
  }
}
