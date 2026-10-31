/** Backend-only SDK. The project relay verifies the desktop's protocol-v2 frames. */
import { BridgeClient, BridgeError } from './index.js';
import type { ActionGroups, AppTool, BridgeTransport } from './index.js';
import { nonce } from './secure.js';
export { BridgeError, BridgeJobError } from './index.js';
export type * from './index.js';
export interface RelayOptions {
  baseURL: string;
  apiKey: string;
  connectionId: string;
  account: { appId: string; appUser: string };
  origin: string;
  groups?: ActionGroups;
  tools?: AppTool[];
  /** Recheck project authorization before every operation, including heartbeats/tools. */
  authorize?: () => Promise<void>;
}
export interface RelayEnrollment { code: string; connectionId: string; expiresAt: string; appId: string; name: string; origin: string }
export interface RelayStatus { connectionId: string; appUser: string; connected: boolean; revoked: boolean }
export interface RelayProject {
  id: string; appId: string; name: string; origin: string; createdAt: string; revoked: boolean;
}
/** Returned once on creation or rotation. Store apiKey as a backend secret. */
export interface RelayProjectCredential { project: RelayProject; apiKey: string }
export interface RelayAdminOptions { baseURL: string; adminKey: string }
function endpoint(raw: string): string {
  if (typeof window !== 'undefined') throw new Error('Relay credentials must stay on the backend.');
  const url = new URL(raw);
  if (url.username || url.password || url.search || url.hash || url.pathname !== '/' ||
      (url.protocol !== 'https:' && !(url.protocol === 'http:' && ['127.0.0.1','[::1]','localhost'].includes(url.hostname))))
    throw new Error('Use an HTTPS relay origin, or loopback HTTP for development.');
  return url.origin;
}
export async function relayRequest<T>(options: Pick<RelayOptions,'baseURL'|'apiKey'>, path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  headers.set('Content-Type', 'application/json');
  headers.set('Authorization', 'Bearer '+options.apiKey);
  const response = await fetch(endpoint(options.baseURL)+path, {...init, redirect:'error',
    headers,
    signal:init.signal ?? AbortSignal.timeout(45000)});
  let data;
  try { data = await response.json(); }
  catch { throw new BridgeError('invalid_response','The relay returned an unreadable response.',response.status,{category:'transport'}); }
  if (!response.ok) throw new BridgeError(data.error?.code ?? 'relay_error', data.error?.message ?? 'Relay request failed.', response.status);
  return data as T;
}
/** Register every project once, including a relay hosting just one website.
 * Operator-only: never give this credential to hosted projects or browsers. */
export class RelayAdminClient {
  private readonly options: Pick<RelayOptions, 'baseURL'|'apiKey'>;
  constructor(options: RelayAdminOptions) {
    this.options = {baseURL: endpoint(options.baseURL), apiKey: options.adminKey};
  }
  createProject(identity: Pick<RelayProject,'appId'|'name'|'origin'>): Promise<RelayProjectCredential> {
    return relayRequest(this.options,'/relay/admin/v1/projects',{method:'POST',body:JSON.stringify(identity)});
  }
  async listProjects(): Promise<RelayProject[]> {
    return (await relayRequest<{projects: RelayProject[]}>(this.options,'/relay/admin/v1/projects')).projects;
  }
  getProject(options: {projectId: string}): Promise<RelayProject> {
    return relayRequest(this.options,this.projectPath(options.projectId));
  }
  rotateProjectKey(options: {projectId: string}): Promise<RelayProjectCredential> {
    return relayRequest(this.options,this.projectPath(options.projectId)+'/rotate-key',{method:'POST'});
  }
  /** Irreversible: cancels connectors and requires a new app ID and fresh desktop consent. */
  revokeProject(options: {projectId: string}): Promise<RelayProject> {
    return relayRequest(this.options,this.projectPath(options.projectId),{method:'DELETE'});
  }
  private projectPath(id: string): string {
    if (!/^[a-f0-9]{64}$/.test(id)) throw new Error('A registered relay project ID is required.');
    return '/relay/admin/v1/projects/'+id;
  }
}
export class RelayClient extends BridgeClient {
  constructor(readonly relay: RelayOptions) {
    const base = endpoint(relay.baseURL);
    const identity = {deviceId:'',publicKey:'',endpoint:base};
    if (!/^[a-f0-9]{64}$/.test(relay.connectionId) || !relay.account.appUser) throw new Error('A paired account connection is required.');
    const transport: BridgeTransport = {
      reset() {},
      async fetch(path, init = {}, lease = '') {
        await relay.authorize?.();
        const response = await fetch(base+'/relay/v1/connections/'+relay.connectionId+'/request', {
          method:'POST', redirect:'error', signal:init.signal ?? AbortSignal.timeout(45000),
          headers:{'Content-Type':'application/json',Authorization:'Bearer '+relay.apiKey,'X-WDC-Account':relay.account.appUser},
          body:JSON.stringify({requestId:nonce(),deadline:Date.now()+45000,method:init.method ?? 'GET',path,body:typeof init.body==='string'?init.body:'',lease}),
        });
        if (path === '/v2/capabilities' && response.ok) {
          const data = await response.clone().json();
          if (identity.deviceId && identity.deviceId !== data.pairing.deviceId) throw new BridgeError('verification_failed','Relay device identity changed.',0);
          identity.deviceId = data.pairing.deviceId;
        }
        if (response.status === 503) {
          const data = await response.clone().json().catch(()=>({}));
          if(data.error?.code==='submission_unknown') throw new BridgeError('submission_unknown','The relay could not confirm submission. Recover by the original job key; do not replay.',0,{category:'transport'});
        }
        return response;
      },
      async close() {},
    };
    super({origin:relay.origin,account:relay.account,groups:relay.groups,tools:relay.tools,
      connection:{pairingId:relay.connectionId,identity,privateKey:{} as CryptoKey,publicKey:''}},transport);
  }
  async status(): Promise<RelayStatus> {
    await this.relay.authorize?.();
    return relayRequest(this.relay,'/relay/v1/connections/'+this.relay.connectionId,{headers:{'X-WDC-Account':this.relay.account.appUser}});
  }
  static enroll(options: Pick<RelayOptions,'baseURL'|'apiKey'> & {appUser:string}):Promise<RelayEnrollment>{
    return relayRequest(options,'/relay/v1/enrollments',{method:'POST',body:JSON.stringify({appUser:options.appUser})});
  }
  static revoke(options: Pick<RelayOptions,'baseURL'|'apiKey'|'connectionId'> & {appUser:string}):Promise<{revoked:boolean}>{
    if (!/^[a-f0-9]{64}$/.test(options.connectionId) || !options.appUser) throw new Error('A paired account connection is required.');
    return relayRequest(options,'/relay/v1/connections/'+options.connectionId,{method:'DELETE',headers:{'X-WDC-Account':options.appUser}});
  }
}
