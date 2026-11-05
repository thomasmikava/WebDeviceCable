/** Backend-only SDK. The project relay verifies the desktop's protocol-v2 frames. */
import { BridgeClient, BridgeError, connectionReviewOptions } from './index.js';
import { nonce } from './secure.js';
export { BridgeError, BridgeJobError } from './index.js';
function endpoint(raw) {
    if (typeof window !== 'undefined')
        throw new Error('Relay credentials must stay on the backend.');
    const url = new URL(raw);
    if (url.username || url.password || url.search || url.hash || url.pathname !== '/' ||
        (url.protocol !== 'https:' && !(url.protocol === 'http:' && ['127.0.0.1', '[::1]', 'localhost'].includes(url.hostname))))
        throw new Error('Use an HTTPS relay origin, or loopback HTTP for development.');
    return url.origin;
}
export async function relayRequest(options, path, init = {}) {
    const headers = new Headers(init.headers);
    headers.set('Content-Type', 'application/json');
    headers.set('Authorization', 'Bearer ' + options.apiKey);
    const response = await fetch(endpoint(options.baseURL) + path, { ...init, redirect: 'error',
        headers,
        signal: init.signal ?? AbortSignal.timeout(45000) });
    let data;
    try {
        data = await response.json();
    }
    catch {
        throw new BridgeError('invalid_response', 'The relay returned an unreadable response.', response.status, { category: 'transport' });
    }
    if (!response.ok)
        throw new BridgeError(data.error?.code ?? 'relay_error', data.error?.message ?? 'Relay request failed.', response.status);
    return data;
}
/** Register every project once, including a relay hosting just one website.
 * Operator-only: never give this credential to hosted projects or browsers. */
export class RelayAdminClient {
    options;
    constructor(options) {
        this.options = { baseURL: endpoint(options.baseURL), apiKey: options.adminKey };
    }
    createProject(identity) {
        return relayRequest(this.options, '/relay/admin/v1/projects', { method: 'POST', body: JSON.stringify(identity) });
    }
    async listProjects() {
        return (await relayRequest(this.options, '/relay/admin/v1/projects')).projects;
    }
    getProject(options) {
        return relayRequest(this.options, this.projectPath(options.projectId));
    }
    rotateProjectKey(options) {
        return relayRequest(this.options, this.projectPath(options.projectId) + '/rotate-key', { method: 'POST' });
    }
    /** Irreversible: cancels connectors and requires a new app ID and fresh desktop consent. */
    revokeProject(options) {
        return relayRequest(this.options, this.projectPath(options.projectId), { method: 'DELETE' });
    }
    projectPath(id) {
        if (!/^[a-f0-9]{64}$/.test(id))
            throw new Error('A registered relay project ID is required.');
        return '/relay/admin/v1/projects/' + id;
    }
}
export class RelayClient extends BridgeClient {
    relay;
    constructor(relay) {
        const base = endpoint(relay.baseURL);
        const identity = { deviceId: '', publicKey: '', endpoint: base };
        if (!/^[a-f0-9]{64}$/.test(relay.connectionId) || !relay.account.appUser)
            throw new Error('A paired account connection is required.');
        let checked = false, legacyToolCountLimit, inputSchemaVersion, operationDetailsVersion;
        const transport = {
            reset() { checked = false; },
            checkToolCount(count) { if (legacyToolCountLimit !== undefined && legacyToolCountLimit > 0 && count > legacyToolCountLimit)
                throw new BridgeError('relay_update_required', 'Update the WDC relay to use this tool catalog. This relay still advertises a limit on the number of tools per execution.', 409); },
            checkToolFeatures(schema, operation) { if (schema && inputSchemaVersion !== 1 || operation && operationDetailsVersion !== 1)
                throw new BridgeError('relay_update_required', 'Update the WDC relay to support these tool input schemas and operation details.', 409); },
            async fetch(path, init = {}, lease = '') {
                await relay.authorize?.();
                if (!checked) {
                    let capability;
                    try {
                        capability = await relayRequest(relay, '/relay/v1/capabilities');
                    }
                    catch (error) {
                        if (error instanceof BridgeError && error.status === 404)
                            throw new BridgeError('relay_update_required', 'Update the WDC relay to 0.2.0 or later.', 409);
                        throw error;
                    }
                    if (capability.scopedToolsVersion !== 1)
                        throw new BridgeError('relay_update_required', 'Update the WDC relay to 0.2.0 or later.', 409);
                    legacyToolCountLimit = capability.limits?.toolsPerExecution;
                    inputSchemaVersion = capability.toolInputSchemaVersion;
                    operationDetailsVersion = capability.toolOperationDetailsVersion;
                    checked = true;
                }
                const response = await fetch(base + '/relay/v1/connections/' + relay.connectionId + '/request', {
                    method: 'POST', redirect: 'error', signal: init.signal ?? AbortSignal.timeout(45000),
                    headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + relay.apiKey, 'X-WDC-Account': relay.account.appUser },
                    body: JSON.stringify({ requestId: nonce(), deadline: Date.now() + 45000, method: init.method ?? 'GET', path, body: typeof init.body === 'string' ? init.body : '', lease }),
                });
                if (path === '/v2/capabilities' && response.ok) {
                    const data = await response.clone().json();
                    if (identity.deviceId && identity.deviceId !== data.pairing.deviceId)
                        throw new BridgeError('verification_failed', 'Relay device identity changed.', 0);
                    identity.deviceId = data.pairing.deviceId;
                }
                if (response.status === 503) {
                    const data = await response.clone().json().catch(() => ({}));
                    if (data.error?.code === 'submission_unknown')
                        throw new BridgeError('submission_unknown', 'The relay could not confirm submission. Recover by the original job key; do not replay.', 0, { category: 'transport' });
                }
                return response;
            },
            async close() { },
        };
        super({ minimumRoutingVersion: relay.minimumRoutingVersion, origin: relay.origin, account: relay.account, groups: relay.groups ?? [],
            connection: { pairingId: relay.connectionId, identity, privateKey: {}, publicKey: '' } }, transport);
        this.relay = relay;
    }
    async status() {
        await this.relay.authorize?.();
        return relayRequest(this.relay, '/relay/v1/connections/' + this.relay.connectionId, { headers: { 'X-WDC-Account': this.relay.account.appUser } });
    }
    static enroll(options) {
        const suggestions = connectionReviewOptions(options);
        return (async () => {
            if (options.logoURL || options.requestedPermissions) {
                let capability;
                try {
                    capability = await relayRequest(options, '/relay/v1/capabilities');
                }
                catch (error) {
                    if (error instanceof BridgeError && error.status === 404)
                        throw new BridgeError('relay_update_required', 'Update the WDC relay to 0.2.1 or later to review connection settings.', 409);
                    throw error;
                }
                if (capability.connectionReviewVersion !== 1)
                    throw new BridgeError('relay_update_required', 'Update the WDC relay to 0.2.1 or later to review connection settings.', 409);
            }
            return relayRequest(options, '/relay/v1/enrollments', { method: 'POST', body: JSON.stringify({ appUser: options.appUser, ...suggestions }) });
        })();
    }
    static revoke(options) {
        if (!/^[a-f0-9]{64}$/.test(options.connectionId) || !options.appUser)
            throw new Error('A paired account connection is required.');
        return relayRequest(options, '/relay/v1/connections/' + options.connectionId, { method: 'DELETE', headers: { 'X-WDC-Account': options.appUser } });
    }
}
/** A short-lived invitation opens a local review; it never approves execution. */
export function relayConnectURL(options) {
    if (!/^[a-f0-9]{64}$/.test(options.code))
        throw new Error('A single-use relay pairing code is required.');
    const origin = endpoint(options.baseURL);
    if (new URL(origin).protocol === 'http:' && new URL(origin).hostname === 'localhost')
        throw new Error('Use a numeric loopback relay origin for local pairing.');
    return 'webdevicecable://relay-connect?' + new URLSearchParams({ relayUrl: origin, code: options.code });
}
/** Open the already-paired connection's local program settings. */
export function relaySettingsURL(options) {
    if (!/^[a-f0-9]{64}$/.test(options.connectionId))
        throw new Error('A relay connection ID is required.');
    const origin = endpoint(options.baseURL);
    if (new URL(origin).protocol === 'http:' && new URL(origin).hostname === 'localhost')
        throw new Error('Use a numeric loopback relay origin for local pairing.');
    return 'webdevicecable://settings?' + new URLSearchParams({ relayUrl: origin, connectionId: options.connectionId });
}
