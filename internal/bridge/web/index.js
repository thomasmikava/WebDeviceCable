import { BridgeError, SecureTransport, parseInvitation, createSigningKeys, checkOrigin, localURL, nonce, launchRequest } from './secure.js';
export { ConnectionStore } from './storage.js';
export { BridgeError, BridgeSecurityError } from './secure.js';
const done = (job) => ['completed', 'cancelled', 'timed_out', 'blocked', 'interrupted', 'failed'].includes(job.status);
const sleep = (ms) => new Promise(resolve => setTimeout(resolve, ms));
async function json(response) {
    let body;
    try {
        body = await response.json();
    }
    catch (error) {
        if (networkError(error))
            throw error;
        throw new BridgeError('invalid_response', 'WDC returned an unreadable response. Check the request status before retrying.', response.status, { category: 'transport' });
    }
    if (!response.ok)
        throw new BridgeError(body.error?.code ?? 'http_error', body.error?.message ?? 'Bridge request failed.', response.status, body.error);
    return body;
}
export function connectionReviewOptions(options, origin) {
    if (options.logoURL !== undefined) {
        let url;
        try {
            url = new URL(options.logoURL);
        }
        catch {
            throw new BridgeError('invalid_logo', 'Supply a direct logo HTTPS URL.', 400);
        }
        if (typeof options.logoURL !== 'string' || options.logoURL.length > 2048 || url.username || url.password || url.hash || (url.protocol !== 'https:' && !(url.protocol === 'http:' && ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname) && (!origin || url.origin === origin))))
            throw new BridgeError('invalid_logo', 'Supply a logo HTTPS URL, or the app’s loopback HTTP URL.', 400);
    }
    if (options.requestedPermissions !== undefined && (typeof options.requestedPermissions !== 'object' || options.requestedPermissions === null || Array.isArray(options.requestedPermissions) || Object.entries(options.requestedPermissions).some(([key, value]) => !['automaticApprovals', 'files'].includes(key) || value !== undefined && typeof value !== 'boolean')))
        throw new BridgeError('invalid_permissions', 'automaticApprovals and files must be booleans.', 400);
    return { ...(options.logoURL ? { logoURL: options.logoURL } : {}), ...(options.requestedPermissions ? { requestedPermissions: { ...options.requestedPermissions } } : {}) };
}
export class BridgeJobError extends BridgeError {
    job;
    constructor(job) {
        super(job.error?.code ?? job.code ?? job.status, job.error?.message ?? job.detail ?? 'The request did not complete.', 0, job.error);
        this.job = job;
        this.name = 'BridgeJobError';
    }
}
function appOrigin(value) { if (value)
    return value; if (typeof window !== 'undefined')
    return window.location.origin; throw new BridgeError('origin_required', 'Supply the app origin outside a browser.', 400); }
function groupsArray(groups) {
    if (!Array.isArray(groups))
        return Object.entries(groups).map(([id, name]) => ({ id, name }));
    const result = [];
    const visit = (definitions, parent, depth = 1) => {
        if (depth > 8)
            throw new BridgeError('invalid_groups', 'Group hierarchies support up to 8 levels.', 400);
        for (const definition of definitions) {
            if (result.length >= 64)
                throw new BridgeError('invalid_groups', 'An app can declare up to 64 groups.', 400);
            const id = parent ? `${parent}.${definition.id}` : definition.id;
            result.push({ id, name: definition.name, ...(definition.operation ? { operation: definition.operation } : {}), ...(definition.hidden ? { hidden: true } : {}), ...(definition.replaces?.length ? { replaces: definition.replaces } : {}), ...(definition.kind ? { kind: definition.kind } : {}), ...(parent || definition.parentId ? { parentId: parent ?? definition.parentId } : {}) });
            if (definition.children?.length)
                visit(definition.children, id, depth + 1);
        }
    };
    visit(groups);
    return result;
}
function networkError(error) { return error instanceof TypeError || error instanceof DOMException && ['NetworkError', 'TimeoutError'].includes(error.name) || error instanceof BridgeError && error.category === 'transport'; }
function unknownSubmission(error, key) {
    if (networkError(error)) {
        const failure = new BridgeError('submission_unknown', 'The submission result is unknown. Retry the same request with the same idempotency key; do not create a new request.', 0, { category: 'transport', retryable: true });
        failure.idempotencyKey = key;
        throw failure;
    }
    if (error instanceof BridgeError && error.status === 0)
        error.idempotencyKey = key;
    throw error;
}
const byteLength = (value) => new TextEncoder().encode(value).length;
const validation = (code, message) => new BridgeError(code, message, 400, { category: 'validation' });
function definitions(tools) { return tools.map(({ execute, ...tool }) => tool); }
function captureTools(tools, mode) {
    if (!tools)
        return [];
    if (!Array.isArray(tools))
        throw validation('invalid_tools', 'Tools must be an array.');
    if (tools.length && mode && mode !== 'app-tools' && mode !== 'workspace')
        throw validation('invalid_mode', 'Tools require app-tools or workspace mode.');
    const seen = new Set();
    let definitionBytes = 2;
    try {
        return tools.map(tool => {
            if (!/^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$/.test(tool.id) || tool.id.startsWith('wdc_') || seen.has(tool.id) || typeof tool.execute !== 'function' || !tool.title || byteLength(tool.title) > 100 || byteLength(tool.description) > 2000)
                throw validation('invalid_tools', 'Invalid or duplicate tool definition.');
            seen.add(tool.id);
            const copy = JSON.parse(JSON.stringify({ id: tool.id, title: tool.title, description: tool.description, inputSchema: tool.inputSchema, readOnly: tool.readOnly ?? false, ...(tool.operationDetails !== undefined ? { operationDetails: tool.operationDetails } : {}) }));
            const schemaJSON = JSON.stringify(copy.inputSchema).replaceAll('\u2028', '\\u2028').replaceAll('\u2029', '\\u2029');
            if (byteLength(schemaJSON) > 8192)
                throw validation('invalid_tools', 'Input schema exceeds 8 KiB.');
            if (copy.operationDetails !== undefined && typeof copy.operationDetails !== 'boolean')
                throw validation('invalid_tools', 'operationDetails must be a boolean.');
            validateToolSchema(copy.inputSchema, 0);
            definitionBytes += byteLength(JSON.stringify(copy)) + (seen.size > 1 ? 1 : 0);
            if (definitionBytes > 1048576)
                throw validation('tool_set_too_large', 'Tool definitions exceed 1 MiB.');
            return { ...copy, execute: tool.execute };
        });
    }
    catch (error) {
        if (error instanceof BridgeError)
            throw error;
        throw validation('invalid_tools', 'Tools must contain serializable definitions.');
    }
}
function validateToolSchema(schema, depth) {
    const invalid = () => { throw validation('invalid_tools', 'Input schema must use the supported 8 KiB / 12-level JSON Schema subset.'); };
    if (!schema || typeof schema !== 'object' || Array.isArray(schema) || depth > 12 || Object.keys(schema).some(key => !['type', 'properties', 'required', 'additionalProperties', 'items', 'enum', 'const', 'anyOf', 'description', 'title', 'minimum', 'maximum', 'minLength', 'maxLength', 'minItems', 'maxItems'].includes(key)))
        invalid();
    if (depth === 0 && schema.type !== 'object')
        invalid();
    if ('type' in schema) {
        const types = Array.isArray(schema.type) ? schema.type : [schema.type];
        if (!types.length || types.length > 7 || new Set(types).size !== types.length || types.some(type => !['object', 'array', 'string', 'number', 'integer', 'boolean', 'null'].includes(type)))
            invalid();
    }
    for (const key of ['title', 'description'])
        if (key in schema && typeof schema[key] !== 'string')
            invalid();
    if ('enum' in schema && (!Array.isArray(schema.enum) || !schema.enum.length || schema.enum.length > 64))
        invalid();
    if ('properties' in schema) {
        const properties = schema.properties;
        if (!properties || typeof properties !== 'object' || Array.isArray(properties) || Object.keys(properties).length > 64)
            invalid();
        for (const child of Object.values(properties))
            validateToolSchema(child, depth + 1);
    }
    if ('required' in schema && (!Array.isArray(schema.required) || schema.required.length > 64 || new Set(schema.required).size !== schema.required.length || schema.required.some(key => typeof key !== 'string')))
        invalid();
    if ('additionalProperties' in schema && typeof schema.additionalProperties !== 'boolean')
        validateToolSchema(schema.additionalProperties, depth + 1);
    if ('items' in schema)
        validateToolSchema(schema.items, depth + 1);
    if ('anyOf' in schema) {
        if (!Array.isArray(schema.anyOf) || !schema.anyOf.length || schema.anyOf.length > 16)
            invalid();
        for (const child of schema.anyOf)
            validateToolSchema(child, depth + 1);
    }
    for (const [low, high] of [['minimum', 'maximum'], ['minLength', 'maxLength'], ['minItems', 'maxItems']]) {
        for (const key of [low, high])
            if (key in schema && (typeof schema[key] !== 'number' || !Number.isFinite(schema[key]) || key !== 'minimum' && key !== 'maximum' && (!Number.isSafeInteger(schema[key]) || schema[key] < 0)))
                invalid();
        if (low in schema && high in schema && schema[low] > schema[high])
            invalid();
    }
}
function legacyToolSchema(schema) {
    if (Object.keys(schema).some(key => !['type', 'properties', 'required', 'additionalProperties', 'items', 'enum', 'description', 'title'].includes(key)))
        return false;
    if (schema.type === 'object') {
        const properties = schema.properties;
        return !!properties && schema.additionalProperties === false && Array.isArray(schema.required) && schema.required.length === Object.keys(properties).length && schema.required.every(key => typeof key === 'string' && Object.hasOwn(properties, key)) && Object.values(properties).every(legacyToolSchema);
    }
    return schema.type === 'array' ? !!schema.items && legacyToolSchema(schema.items) : typeof schema.type === 'string' && ['string', 'number', 'integer', 'boolean', 'null'].includes(schema.type);
}
function serialize(value) { try {
    return JSON.stringify(value);
}
catch {
    throw validation('invalid_request', 'Request fields must be serializable JSON.');
} }
function creationBody(value, admission = false) {
    const owner = (admission ? value.request : value);
    const toolJSON = JSON.stringify(owner.tools ?? []);
    if (byteLength(toolJSON) > 1048576)
        throw validation('tool_set_too_large', 'Tool definitions exceed 1 MiB.');
    const { tools: _tools, ...ordinary } = owner;
    if (byteLength(serialize(admission ? { ...value, request: ordinary } : ordinary)) > 40960)
        throw validation('request_too_large', 'Non-tool request fields exceed 40 KiB.');
    const body = serialize(value);
    if (byteLength(body) > 1088 * 1024)
        throw validation('request_too_large', 'Execution creation exceeds 1,088 KiB.');
    return body;
}
export class BridgeClient {
    options;
    transport;
    legacyToolCountLimit;
    toolInputSchemaVersion;
    toolOperationDetailsVersion;
    executions = new Map();
    filePermissionsVersion;
    textContextVersion;
    toolResultFilesVersion;
    sessions = new Map();
    activeCalls = new Set();
    callbackCount = 0;
    toolsBusy = false;
    toolsTimer;
    lifetime = new AbortController();
    generation = 0;
    lease = '';
    leaseExpiresAt = 0;
    connecting;
    timer;
    heartbeatPending = false;
    closed = true;
    watchers = new Set();
    onPageHide = () => { void this.close().catch(() => { }); };
    constructor(options, transport) {
        this.options = options;
        const keys = options.connection;
        if (!keys || !/^[a-f0-9]{64}$/.test(keys.pairingId))
            throw new Error('A signed protocol v2 pairing is required. Re-pair this app.');
        this.transport = transport ?? new SecureTransport(keys.identity, appOrigin(options.origin), keys, keys.pairingId, options.baseURL);
    }
    static async pair(options) {
        return BridgeClient.pairWithKeys(options, await createSigningKeys());
    }
    static async pairWithKeys(options, keys) {
        const origin = appOrigin(options.origin);
        checkOrigin(origin);
        const invite = parseInvitation(options.invitation);
        if (invite.appId !== options.appId || invite.appUser !== options.appUser || invite.origin !== origin)
            throw new BridgeError('identity_mismatch', 'Invitation does not match this app, account or origin.', 403);
        const identity = { deviceId: invite.deviceId, publicKey: invite.publicKey, endpoint: invite.endpoint };
        const transport = new SecureTransport(identity, origin, keys, '', options.baseURL);
        try {
            const result = await json(await transport.fetch('/v2/pairings/claim', { method: 'POST', body: JSON.stringify({ appId: options.appId, appUser: options.appUser }) }, '', invite.secret));
            if (result.pairing.appId !== options.appId || result.pairing.appUser !== options.appUser || result.pairing.origin !== origin || result.pairing.deviceId !== identity.deviceId || !/^[a-f0-9]{64}$/.test(result.pairing.id))
                throw new BridgeError('identity_mismatch', 'Verified pairing identity mismatch.', 403);
            return { pairing: result.pairing, connection: { ...keys, identity, pairingId: result.pairing.id } };
        }
        finally {
            await transport.close().catch(() => { });
        }
    }
    /** Call directly from a click handler. Approval happens in the authenticated local panel. */
    static async requestPairing(options) {
        options = { ...options, ...connectionReviewOptions(options, typeof window !== 'undefined' ? window.location.origin : undefined) };
        if (options.launchInstalled !== false && typeof document !== 'undefined')
            return BridgeClient.launchPairing(options);
        if (typeof window === 'undefined')
            throw new BridgeError('browser_required', 'Interactive pairing requires a browser.', 400);
        const endpoint = localURL(options.baseURL ?? 'http://127.0.0.1:47321'), requestId = nonce();
        if (!options.appId || options.appId.length > 100 || !options.name || options.name.length > 100 || !options.appUser || options.appUser.length > 200)
            throw new BridgeError('invalid_pairing', 'App ID, name and account are required.', 400);
        const timeout = options.timeoutMs ?? 300000;
        if (!Number.isFinite(timeout) || timeout < 1000 || timeout > 300000)
            throw new BridgeError('invalid_pairing', 'Pairing timeout must be 1–300 seconds.', 400);
        const popup = window.open(`${endpoint}/#connect-request=${requestId}`, '_blank', 'popup,width=520,height=640');
        if (!popup)
            throw new BridgeError('popup_blocked', 'Allow the WebDeviceCable connection window, then try again.', 0, { category: 'permission' });
        return new Promise((resolve, reject) => {
            let settling = false;
            const cleanup = () => { clearTimeout(deadline); clearInterval(closed); window.removeEventListener('message', receive); };
            const fail = (error) => { if (settling)
                return; settling = true; cleanup(); popup.close(); reject(error); };
            const receive = (event) => {
                if (event.source !== popup || event.origin !== endpoint || event.data?.requestId !== requestId)
                    return;
                if (event.data.type === 'wdc:pairing-ready') {
                    if (options.requestedPermissions?.files && event.data.filePermissionsVersion !== 1) {
                        fail(new BridgeError('desktop_update_required', 'Update WDC desktop to review file access.', 409));
                        return;
                    }
                    if ((options.logoURL || options.requestedPermissions) && event.data.connectionReviewVersion !== 1) {
                        fail(new BridgeError('desktop_update_required', 'Update WDC desktop to 0.2.1 or later to review this app’s logo and requested permissions.', 409));
                        return;
                    }
                    popup.postMessage({ type: 'wdc:pairing-request', requestId, appId: options.appId, name: options.name, appUser: options.appUser, ...connectionReviewOptions(options, window.location.origin) }, endpoint);
                }
                if (event.data.type === 'wdc:pairing-rejected')
                    fail(new BridgeError('pairing_declined', 'Connection declined in WebDeviceCable.', 403, { category: 'permission' }));
                if (event.data.type === 'wdc:pairing-approved' && typeof event.data.invitation === 'string' && !settling) {
                    settling = true;
                    cleanup();
                    BridgeClient.pair({ invitation: event.data.invitation, appId: options.appId, appUser: options.appUser, origin: window.location.origin, baseURL: endpoint }).then(result => {
                        popup.postMessage({ type: 'wdc:pairing-connected', requestId }, endpoint);
                        resolve(result);
                    }, error => { popup.postMessage({ type: 'wdc:pairing-failed', requestId }, endpoint); reject(error); });
                }
            };
            const deadline = setTimeout(() => fail(new BridgeError('pairing_timeout', 'The connection request expired. Try again.', 408, { category: 'timeout' })), timeout);
            const closed = setInterval(() => { if (popup.closed)
                fail(new BridgeError('pairing_declined', 'The connection window was closed.', 403, { category: 'permission' })); }, 300);
            window.addEventListener('message', receive);
        });
    }
    static async launchPairing(options) {
        const endpoint = localURL(options.baseURL ?? 'http://127.0.0.1:47321'), origin = window.location.origin, requestId = nonce();
        checkOrigin(origin);
        if (!options.appId || options.appId.length > 100 || !options.name || options.name.length > 100 || !options.appUser || options.appUser.length > 200)
            throw new BridgeError('invalid_pairing', 'App ID, name and account are required.', 400);
        const timeout = options.timeoutMs ?? 300000;
        if (!Number.isFinite(timeout) || timeout < 1000 || timeout > 300000)
            throw new BridgeError('invalid_pairing', 'Pairing timeout must be 1–300 seconds.', 400);
        const popup = window.open('about:blank', '_blank', 'popup,width=460,height=420');
        if (!popup)
            throw new BridgeError('popup_blocked', 'Allow the connection window, then try again.', 0, { category: 'permission' });
        const doc = popup.document;
        doc.title = 'Connect to WebDeviceCable';
        doc.documentElement.style.colorScheme = 'dark';
        doc.body.style.cssText = 'font:16px system-ui;margin:48px;line-height:1.5';
        const heading = doc.createElement('h1');
        heading.textContent = 'Connect this device';
        heading.style.fontSize = '24px';
        const status = doc.createElement('p');
        status.textContent = 'Open WebDeviceCable and approve this connection.';
        const link = doc.createElement('a');
        link.textContent = 'Open WebDeviceCable';
        link.style.cssText = 'display:inline-block;padding:12px 18px;border:1px solid currentColor;border-radius:8px;color:inherit;text-decoration:none';
        const cancel = doc.createElement('button');
        cancel.textContent = 'Cancel';
        cancel.style.cssText = 'display:block;margin-top:28px';
        cancel.onclick = () => popup.close();
        doc.body.append(heading, status, link, cancel);
        const keys = await createSigningKeys();
        link.href = 'webdevicecable://connect?' + new URLSearchParams({ requestId, origin, publicKey: keys.publicKey });
        link.click();
        let pinned;
        const deadline = Date.now() + timeout;
        try {
            while (Date.now() < deadline && !popup.closed) {
                try {
                    const result = await launchRequest(endpoint, origin, keys, { requestId, appId: options.appId, name: options.name, appUser: options.appUser, ...connectionReviewOptions(options, origin) }, pinned);
                    pinned = result.identity;
                    status.textContent = 'Waiting for your approval in WebDeviceCable…';
                    if (result.status === 'rejected')
                        throw new BridgeError('pairing_declined', 'Connection declined in WebDeviceCable.', 403, { category: 'permission' });
                    if (result.status === 'approved')
                        return await BridgeClient.pairWithKeys({ invitation: result.invitation, appId: options.appId, appUser: options.appUser, origin, baseURL: endpoint }, keys);
                }
                catch (error) {
                    if (error instanceof BridgeError && error.code !== 'launch_required' || !(error instanceof BridgeError) && !networkError(error))
                        throw error;
                }
                await sleep(650);
            }
            throw new BridgeError(popup.closed ? 'pairing_declined' : 'pairing_timeout', popup.closed ? 'The connection window was closed.' : 'The connection request expired. Try again.', 408);
        }
        finally {
            popup.close();
        }
    }
    async request(path, init = {}, lease = this.lease) {
        try {
            return await this.transport.fetch(path, init, lease);
        }
        catch (error) {
            this.transport.reset();
            if (networkError(error))
                throw new BridgeError('connection_failed', 'Cannot reach the local runner. Check that WebDeviceCable is running.', 0, { category: 'transport', retryable: true });
            throw error;
        }
    }
    get connected() { return !this.closed && Date.now() < this.leaseExpiresAt; }
    permissions() { return this.request('/v2/permissions').then((json)); }
    permissionRequest(id) { return this.request(`/v2/permissions/${encodeURIComponent(id)}`).then((json)); }
    async requestPermissions(request, options = {}) {
        await this.connect();
        if (request.files && this.filePermissionsVersion !== 1)
            throw new BridgeError('desktop_update_required', 'Update WebDeviceCable to manage file access.', 409);
        let result = await this.request('/v2/permissions', { method: 'POST', body: JSON.stringify(request) }).then((json));
        if (options.wait === false)
            return result;
        while (result.status === 'pending' && !this.closed) {
            if (options.signal?.aborted)
                throw options.signal.reason ?? new DOMException('Aborted', 'AbortError');
            await sleep(650);
            result = await this.permissionRequest(result.id);
        }
        if (result.status !== 'completed')
            throw new BridgeError('permission_denied', 'The requested access was not approved in WebDeviceCable.', 403, { category: 'permission' });
        return result;
    }
    pollTools() {
        if (this.toolsTimer || this.closed || !this.executions.size)
            return;
        this.toolsTimer = setInterval(() => { void this.drainTools(); }, 250);
        void this.drainTools();
    }
    release(key, owner) {
        const entry = this.executions.get(key);
        if (owner && entry !== owner)
            return;
        entry?.controller.abort();
        this.executions.delete(key);
        if (!this.executions.size) {
            clearInterval(this.toolsTimer);
            this.toolsTimer = undefined;
        }
    }
    observe(job) {
        if (!job.request)
            return job;
        const entry = this.executions.get(job.request.idempotencyKey);
        if (entry) {
            if (entry.jobId && entry.jobId !== job.id || entry.hash && job.request.toolSetHash && entry.hash !== job.request.toolSetHash)
                throw new BridgeError('execution_mismatch', 'WDC execution identity changed.', 0, { category: 'security' });
            entry.jobId = job.id;
            entry.hash = job.request.toolSetHash ?? entry.hash;
            if (done(job))
                this.release(job.request.idempotencyKey);
            if (done(job) && job.status !== "completed" && job.request.conversationId)
                this.sessions.delete(job.request.conversationId);
        }
        return job;
    }
    install(key, tools, fingerprint, conversationId, hash) {
        const previous = this.executions.get(key);
        if (previous) {
            if (previous.fingerprint !== fingerprint || previous.conversationId !== conversationId || hash && previous.hash && previous.hash !== hash)
                throw new BridgeError('idempotency_conflict', 'This key has different execution tools or input.', 409);
            previous.submissions++;
            return previous;
        }
        if (!tools.length)
            return;
        if (this.executions.size >= 256)
            throw new BridgeError('execution_limit', 'Resolve pending tool executions before submitting more.', 429);
        const entry = { tools: new Map(tools.map(tool => [tool.id, tool])), fingerprint, conversationId, hash, seen: new Set(), submissions: 1, uncertain: false, controller: new AbortController() };
        this.executions.set(key, entry);
        this.pollTools();
        return entry;
    }
    settleSubmission(key, entry, uncertain) {
        if (!entry)
            return;
        entry.submissions--;
        entry.uncertain ||= uncertain;
        // Matching retries share a snapshot. A rejected attempt cannot discard it
        // while another attempt is pending, accepted, or has an unknown outcome.
        if (!entry.submissions && !entry.jobId && !entry.uncertain)
            this.release(key, entry);
    }
    async drainTools() {
        if (this.closed || this.toolsBusy || !this.executions.size)
            return;
        this.toolsBusy = true;
        try {
            const result = await this.request('/v2/tool-calls/poll', { method: 'POST', body: JSON.stringify({ requests: [...this.executions.keys()] }) }).then((json));
            for (const item of result.completed ?? []) {
                const entry = this.executions.get(item.requestIdempotencyKey);
                if (entry && (!entry.jobId || entry.jobId === item.jobId) && (!entry.hash || entry.hash === item.toolSetHash))
                    this.release(item.requestIdempotencyKey);
            }
            for (const call of result.calls) {
                if (this.callbackCount >= 4)
                    break;
                const entry = this.executions.get(call.requestIdempotencyKey), tool = entry?.tools.get(call.name);
                if (!entry || !tool || this.closed || this.activeCalls.has(call.id) || entry.seen.has(call.id) || !call.jobId || !call.toolSetHash || entry.conversationId !== call.conversationId || entry.jobId && entry.jobId !== call.jobId || entry.hash && entry.hash !== call.toolSetHash)
                    continue;
                entry.jobId = call.jobId;
                entry.hash = call.toolSetHash;
                entry.seen.add(call.id);
                this.activeCalls.add(call.id);
                this.callbackCount++;
                void this.dispatchTool(call, entry, tool).finally(() => { this.activeCalls.delete(call.id); this.callbackCount--; });
            }
        }
        catch { /* Ambiguous claims and callbacks are never replayed. */ }
        finally {
            this.toolsBusy = false;
        }
    }
    async dispatchTool(call, entry, tool) {
        try {
            // A claim is attempted once. Only its acknowledged owner executes the handler.
            await this.request(`/v2/tool-calls/${call.id}/claim`, { method: 'POST' }).then(json);
            if (this.closed || entry.controller.signal.aborted)
                return;
            let result;
            try {
                result = await tool.execute(call.arguments, { idempotencyKey: call.id, jobId: call.jobId, conversationId: call.conversationId, requestIdempotencyKey: call.requestIdempotencyKey, toolSetHash: call.toolSetHash, signal: entry.controller.signal, reportOperation: operation => this.reportOperation(call, entry, operation) });
                if (result === undefined)
                    result = null;
            }
            catch {
                result = { error: 'The application could not complete this tool call.' };
            }
            try {
                if (byteLength(JSON.stringify(result)) > 32768)
                    result = { error: 'Tool result exceeds the 32 KiB limit. Return a summary or file reference.' };
            }
            catch {
                result = { error: 'Tool result must be serializable JSON.' };
            }
            if (!this.closed && !entry.controller.signal.aborted) {
                try {
                    await this.request(`/v2/tool-calls/${call.id}/result`, { method: 'POST', body: JSON.stringify({ result }) }).then(json);
                }
                catch (error) {
                    // A verified validation rejection did not accept the file result. Do
                    // not rerun the handler; deliver its JSON plus an explicit file error.
                    if (!result || typeof result !== 'object' || !('$wdc' in result) || result.$wdc !== 'tool-result' || !(error instanceof BridgeError) || ![400, 403].includes(error.status) || !['invalid_input', 'invalid_result', 'files_not_granted'].includes(error.code) || this.closed || entry.controller.signal.aborted)
                        throw error;
                    let fallback = { result: 'result' in result ? result.result : null, fileDelivery: { ok: false, code: error.code, message: 'The application tool may have completed, but its files could not be delivered. Do not claim to have inspected them or repeat the tool automatically.' } };
                    if (byteLength(JSON.stringify(fallback)) > 32768)
                        fallback = { error: 'The application tool may have completed, but its file result could not be delivered. Never repeat it automatically.' };
                    await this.request(`/v2/tool-calls/${call.id}/result`, { method: 'POST', body: JSON.stringify({ result: fallback }) }).then(json);
                }
            }
        }
        catch { /* No automatic replay of uncertain tool operations. */ }
    }
    async reportOperation(call, entry, operation) {
        if (this.closed || entry.controller.signal.aborted)
            throw new BridgeError('execution_ended', 'This execution has ended.', 410);
        if (this.toolOperationDetailsVersion !== 1)
            throw new BridgeError('desktop_update_required', 'Update WebDeviceCable to report operation details.', 409);
        const text = (value, limit, required = false) => typeof value === 'string' && byteLength(value) <= limit && (!required || !!value.trim()) && !/[\u0000-\u001f\u007f-\u009f]/u.test(value);
        if (!operation || !text(operation.id, 128, true) || !text(operation.title, 200, true) || operation.version !== undefined && !text(operation.version, 128))
            throw validation('invalid_operation', 'Supply an operation ID, title and optional version within their limits.');
        await this.request(`/v2/tool-calls/${call.id}/operation`, { method: 'POST', body: JSON.stringify({ id: operation.id, title: operation.title, ...(operation.version !== undefined ? { version: operation.version } : {}) }), signal: entry.controller.signal }).then(json);
    }
    registerGroups(groups) { return this.request('/v2/groups', { method: 'POST', body: JSON.stringify({ groups: groupsArray(groups) }) }).then((json)); }
    groups() { return this.request('/v2/groups').then((json)); }
    session(options = {}) { return new BridgeSession(this, options); }
    async run(input, signal) {
        if (signal?.aborted)
            throw signal.reason ?? new DOMException('Aborted', 'AbortError');
        const proposed = typeof input === 'string' ? { prompt: input } : input;
        const request = { ...proposed, tools: captureTools(proposed.tools, proposed.mode) };
        await this.connect();
        const job = await this.submit({ ...request, kind: request.kind ?? (request.action ? 'command' : request.provider ? 'text' : 'action'), idempotencyKey: request.idempotencyKey ?? crypto.randomUUID() });
        let result = job;
        for await (const update of this.stream(job.id, signal))
            result = update;
        if (result.status !== 'completed')
            throw new BridgeJobError(result);
        return result;
    }
    async openSession(options) {
        const tools = captureTools(options.tools, options.mode);
        const { id: _id, tools: _tools, ...selection } = options;
        const body = creationBody({ ...selection, tools: definitions(tools) });
        await this.connect();
        this.checkTools(tools);
        const generation = this.generation;
        const conversation = await this.request('/v2/conversations', { method: 'POST', body }).then((json));
        if (generation !== this.generation || !this.connected)
            throw new BridgeError('lease_closed', 'The tab connection ended while this session was opening.', 410);
        this.sessions.set(conversation.id, { tools, hash: conversation.toolSetHash });
        return conversation;
    }
    async sendSession(id, input) {
        if ("tools" in input)
            throw validation("invalid_tools", "Session messages cannot replace tools.");
        const request = { ...input, idempotencyKey: input.idempotencyKey ?? crypto.randomUUID() };
        const body = serialize(request);
        if (byteLength(body) > 40960)
            throw validation('request_too_large', 'Message exceeds 40 KiB.');
        this.checkExecutionInputs(input);
        const scope = this.sessions.get(id);
        const installed = this.install(request.idempotencyKey, scope?.tools ?? [], body, id, scope?.hash);
        let uncertain = false;
        try {
            return this.observe(await this.request(`/v2/conversations/${encodeURIComponent(id)}/turns`, { method: 'POST', body }).then((json)));
        }
        catch (error) {
            uncertain = !(error instanceof BridgeError && error.status > 0 && !networkError(error));
            return unknownSubmission(error, request.idempotencyKey);
        }
        finally {
            this.settleSubmission(request.idempotencyKey, installed, uncertain);
        }
    }
    async attachSession(id, options) {
        if (this.sessions.has(id))
            return;
        if (options.tools?.length)
            throw validation('invalid_session', 'Tool-enabled sessions cannot attach new handlers. Keep the original session or create a history-backed replacement.');
        const conversation = await this.getSession(id);
        if (conversation.status !== 'open')
            throw new BridgeError('session_closed', 'This session has ended.', 410);
    }
    releaseSession(id) {
        for (const [key, entry] of this.executions)
            if (entry.conversationId === id)
                this.release(key);
        this.sessions.delete(id);
    }
    async getSession(id) {
        const conversation = await this.request(`/v2/conversations/${encodeURIComponent(id)}`).then((json));
        if (conversation.status !== "open")
            this.releaseSession(id);
        return conversation;
    }
    async closeSession(id) {
        this.releaseSession(id);
        return this.request(`/v2/conversations/${encodeURIComponent(id)}`, { method: 'DELETE' }).then((json));
    }
    capabilities() { return this.request('/v2/capabilities').then((json)); }
    async preflight(request, features = []) {
        const tools = captureTools(request.tools, request.mode);
        const body = creationBody({ request: { ...request, tools: definitions(tools), idempotencyKey: 'preflight-check' }, features }, true);
        await this.connect();
        this.checkTools(tools);
        this.checkExecutionInputs(request, features);
        return this.request('/v2/admission', { method: 'POST', body }).then((json));
    }
    /** File bytes stay unchanged and scoped to this requesting tab. */
    async upload(file, name = 'input') {
        await this.connect();
        if (this.filePermissionsVersion !== 1)
            throw new BridgeError('desktop_update_required', 'Update WebDeviceCable to approve file access before uploading files.', 409);
        return this.uploadInput(file, name, '/v2/inputs');
    }
    /** Large prompt text, never a local file or custom-program file manifest. */
    async uploadContext(text) {
        if (typeof text !== 'string' || text.includes('\0') || byteLength(text) > 128 * 1024)
            throw validation('invalid_context', 'Text context must be NUL-free UTF-8 of at most 128 KiB.');
        await this.connect();
        if (this.textContextVersion !== 1)
            throw new BridgeError('desktop_update_required', 'Update WebDeviceCable to send large text context without file access.', 409);
        return this.uploadInput(new Blob([text], { type: 'text/plain' }), 'Conversation context', '/v2/contexts');
    }
    /** Hold the supplied uploads until the owning execution finishes. */
    toolResult(result, inputs) {
        if (this.toolResultFilesVersion !== 1)
            throw new BridgeError('desktop_update_required', 'Update WebDeviceCable to return files to the chat AI.', 409);
        if (!Array.isArray(inputs) || inputs.length < 1 || inputs.length > 8 || new Set(inputs).size !== inputs.length || inputs.some(id => typeof id !== 'string' || !/^[a-f0-9]{64}$/.test(id)))
            throw validation('invalid_input', 'Supply 1–8 completed upload IDs.');
        return { $wdc: 'tool-result', result, inputs: [...inputs] };
    }
    async uploadInput(file, name, path) {
        if (file.size < 0 || file.size > 8 * 1024 * 1024)
            throw new BridgeError('invalid_input', 'Files must be at most 8 MiB.', 400);
        const data = new Uint8Array(await file.arrayBuffer()), sha256 = Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', data))).map(b => b.toString(16).padStart(2, '0')).join('');
        const input = await this.request(path, { method: 'POST', body: JSON.stringify({ name, mediaType: file.type, size: data.length, sha256 }) }).then((json));
        try {
            for (let offset = 0; offset < data.length; offset += 24576) {
                const part = data.subarray(offset, offset + 24576);
                let binary = '';
                for (const byte of part)
                    binary += String.fromCharCode(byte);
                await this.request(`/v2/inputs/${input.id}`, { method: 'PUT', body: JSON.stringify({ offset, data: btoa(binary) }) }).then(json);
            }
            return input;
        }
        catch (error) {
            await this.removeInput(input.id).catch(() => { });
            throw error;
        }
    }
    removeInput(id) { return this.request(`/v2/inputs/${encodeURIComponent(id)}`, { method: 'DELETE' }).then((json)); }
    /** Signed/encrypted chunks, checked again against the signed file hash. */
    async artifact(job, artifact = job.artifacts?.[0]) {
        await this.connect();
        this.checkExecutionInputs({ kind: "image-generate" });
        if (!artifact || !Number.isSafeInteger(artifact.size) || artifact.size < 0 || artifact.size > 8 * 1024 * 1024)
            throw new BridgeError('invalid_artifact', 'Invalid output file.', 400);
        const data = new Uint8Array(artifact.size);
        let offset = 0;
        while (offset < data.length) {
            const part = await this.request(`/v2/jobs/${encodeURIComponent(job.id)}/artifacts/${encodeURIComponent(artifact.id)}/${offset}`).then((json));
            const bytes = Uint8Array.from(atob(part.data), c => c.charCodeAt(0));
            if (part.id !== artifact.id || part.offset !== offset || bytes.length < 1 || bytes.length > 24576 || offset + bytes.length > data.length || part.complete !== (offset + bytes.length === data.length))
                throw new BridgeError('verification_failed', 'Invalid output file chunk.', 0);
            data.set(bytes, offset);
            offset += bytes.length;
        }
        const hash = Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', data))).map(b => b.toString(16).padStart(2, '0')).join('');
        if (hash !== artifact.sha256)
            throw new BridgeError('verification_failed', 'Output file hash mismatch.', 0);
        return new Blob([data], { type: artifact.mediaType });
    }
    actions() { return this.request('/v2/actions').then((json)); }
    /** One instance/lease per requesting tab. Do not share a lease between tabs. */
    async connect() {
        if (this.connecting)
            return this.connecting;
        if (this.connected)
            return;
        this.transport.reset();
        if (!this.closed)
            this.stop();
        const generation = ++this.generation;
        this.connecting = (async () => {
            try {
                {
                    const capabilities = await this.capabilities();
                    if (capabilities.scopedToolsVersion !== 1)
                        throw new BridgeError('desktop_update_required', 'Update WebDeviceCable to 0.2.0 or later before using this SDK.', 409);
                    this.legacyToolCountLimit = capabilities.limits?.toolsPerExecution;
                    this.toolInputSchemaVersion = capabilities.toolInputSchemaVersion;
                    this.toolOperationDetailsVersion = capabilities.toolOperationDetailsVersion;
                    this.filePermissionsVersion = capabilities.filePermissionsVersion;
                    this.textContextVersion = capabilities.textContextVersion;
                    this.toolResultFilesVersion = capabilities.toolResultFilesVersion;
                    const { pairing } = capabilities;
                    if (this.options.minimumRoutingVersion && (capabilities.routingVersion ?? 1) < this.options.minimumRoutingVersion)
                        throw new BridgeError('desktop_update_required', 'Update WebDeviceCable before configuring workflow models and reasoning.', 409);
                    if (this.options.account && (pairing.appId !== this.options.account.appId || pairing.appUser !== this.options.account.appUser || pairing.origin !== appOrigin(this.options.origin) || pairing.deviceId !== this.options.connection.identity.deviceId))
                        throw new BridgeError('identity_mismatch', 'This connection belongs to another app or account. Disconnect and pair this account.', 403);
                }
                await this.openLease(generation);
                if (this.options.groups)
                    await this.registerGroups(this.options.groups);
            }
            catch (error) {
                await this.close().catch(() => { });
                throw error;
            }
        })();
        try {
            await this.connecting;
        }
        finally {
            this.connecting = undefined;
        }
    }
    async openLease(generation) {
        const result = await json(await this.request('/v2/lease', { method: 'POST' }));
        if (generation !== this.generation) {
            await this.request('/v2/lease', { method: 'DELETE', keepalive: true }, result.lease).catch(() => { });
            throw new BridgeError('lease_closed', 'The connection was closed before it finished opening.', 410);
        }
        this.lease = result.lease;
        this.closed = false;
        this.lifetime = new AbortController();
        this.leaseExpiresAt = result.expiresAt ? Date.parse(result.expiresAt) : Date.now() + 12000;
        this.timer = setInterval(() => { void this.heartbeat(); }, 3000);
        if (typeof window !== 'undefined')
            window.addEventListener('pagehide', this.onPageHide);
    }
    async heartbeat() {
        if (this.closed || this.heartbeatPending)
            return;
        if (Date.now() >= this.leaseExpiresAt) {
            this.stop();
            return;
        }
        this.heartbeatPending = true;
        const generation = this.generation;
        try {
            const response = await this.request('/v2/lease', { method: 'PUT', signal: AbortSignal.timeout(5000) });
            if (generation !== this.generation || this.closed)
                return;
            if (!response.ok) {
                this.stop();
            }
            else {
                const result = await json(response);
                this.leaseExpiresAt = Date.parse(result.expiresAt);
                for (const id of result.closedSessions ?? [])
                    this.releaseSession(id);
            }
        }
        catch {
            if (generation === this.generation && !this.closed && Date.now() >= this.leaseExpiresAt)
                this.stop();
        }
        finally {
            this.heartbeatPending = false;
        }
    }
    stop() {
        clearInterval(this.timer);
        clearInterval(this.toolsTimer);
        this.toolsTimer = undefined;
        this.closed = true;
        this.lifetime.abort();
        for (const key of this.executions.keys())
            this.release(key);
        this.sessions.clear();
        if (typeof window !== 'undefined')
            window.removeEventListener('pagehide', this.onPageHide);
        for (const controller of this.watchers)
            controller.abort();
        this.watchers.clear();
    }
    async close() {
        this.generation++;
        const lease = this.lease;
        this.lease = '';
        this.stop();
        try {
            if (lease)
                await json(await this.request('/v2/lease', { method: 'DELETE', keepalive: true }, lease));
        }
        finally {
            await this.transport.close();
        }
    }
    async submit(request) {
        if (this.closed)
            throw new BridgeError('lease_required', 'Call connect() for this requesting tab first.', 410);
        const tools = captureTools(request.tools, request.mode);
        const body = creationBody({ ...request, tools: definitions(tools) });
        this.checkTools(tools);
        this.checkExecutionInputs(request);
        const installed = this.install(request.idempotencyKey, tools, body);
        let uncertain = false;
        try {
            return this.observe(await json(await this.request('/v2/jobs', { method: 'POST', body })));
        }
        catch (error) {
            uncertain = !(error instanceof BridgeError && error.status > 0 && !networkError(error));
            return unknownSubmission(error, request.idempotencyKey);
        }
        finally {
            this.settleSubmission(request.idempotencyKey, installed, uncertain);
        }
    }
    checkExecutionInputs(request, features = []) {
        if ((request.inputs?.length || request.kind === 'image-generate' || request.kind === 'image-edit' || features.some(feature => ['fileTransfer', 'imageInput', 'documentInput', 'imageGeneration', 'imageEditing'].includes(feature))) && this.filePermissionsVersion !== 1)
            throw new BridgeError('desktop_update_required', 'Update WebDeviceCable to approve file access before using files or images.', 409);
        if (request.contexts?.length && this.textContextVersion !== 1)
            throw new BridgeError('desktop_update_required', 'Update WebDeviceCable to accept text context.', 409);
    }
    checkTools(tools) {
        const count = tools.length, schema = tools.some(tool => !legacyToolSchema(tool.inputSchema)), operation = tools.some(tool => tool.operationDetails);
        this.transport.checkToolFeatures?.(schema, operation);
        if (schema && this.toolInputSchemaVersion !== 1 || operation && this.toolOperationDetailsVersion !== 1)
            throw new BridgeError('desktop_update_required', 'Update WebDeviceCable to support these tool input schemas and operation details.', 409);
        this.transport.checkToolCount?.(count);
        if (this.legacyToolCountLimit !== undefined && this.legacyToolCountLimit > 0 && count > this.legacyToolCountLimit)
            throw new BridgeError('desktop_update_required', 'Update WebDeviceCable desktop to use this tool catalog. This desktop still limits the number of tools per execution.', 409);
    }
    async lookup(idempotencyKey) { const job = (await this.request('/v2/jobs/lookup', { method: 'POST', body: JSON.stringify({ idempotencyKey }) }).then((json))).job; return job ? this.observe(job) : null; }
    get(id) { return this.request(`/v2/jobs/${encodeURIComponent(id)}`).then((json)).then(job => this.observe(job)); }
    cancel(id) { return this.request(`/v2/jobs/${encodeURIComponent(id)}/cancel`, { method: 'POST' }).then((json)).then(job => this.observe(job)); }
    /** Full job snapshots, reconnectable without replaying inference or app actions. */
    async *stream(id, signal) {
        const controller = new AbortController();
        this.watchers.add(controller);
        const abort = () => controller.abort();
        signal?.addEventListener('abort', abort, { once: true });
        if (signal?.aborted)
            controller.abort();
        let revision = 0;
        try {
            while (!this.closed && !controller.signal.aborted) {
                let response;
                try {
                    response = await this.request(`/v2/jobs/${encodeURIComponent(id)}/events`, { signal: controller.signal });
                }
                catch (error) {
                    if (this.closed || controller.signal.aborted || !networkError(error))
                        throw error;
                    await sleep(750);
                    continue;
                }
                if (!response.ok) {
                    await json(response);
                    return;
                }
                if (!response.body)
                    throw new Error('Streaming body unavailable.');
                const reader = response.body.getReader();
                const decoder = new TextDecoder();
                let buffer = '';
                try {
                    while (true) {
                        const chunk = await reader.read();
                        if (chunk.done)
                            break;
                        buffer += decoder.decode(chunk.value, { stream: true });
                        if (buffer.length > 52 * 1024 * 1024)
                            throw new Error('Bridge event exceeds size limit.');
                        let boundary;
                        while ((boundary = buffer.indexOf('\n\n')) >= 0) {
                            const event = buffer.slice(0, boundary);
                            buffer = buffer.slice(boundary + 2);
                            const data = event.split('\n').find(line => line.startsWith('data: '));
                            if (!data)
                                continue;
                            const job = this.observe(JSON.parse(data.slice(6)));
                            if (job.revision > revision) {
                                revision = job.revision;
                                yield job;
                            }
                            if (done(job))
                                return;
                        }
                    }
                }
                catch (error) {
                    if (this.closed || controller.signal.aborted)
                        throw error;
                    if (!networkError(error))
                        throw error;
                }
                finally {
                    await reader.cancel().catch(() => { });
                    reader.releaseLock();
                }
                await sleep(250);
            }
            throw new BridgeError('lease_closed', 'This requesting tab no longer has an active lease.', 410);
        }
        finally {
            this.watchers.delete(controller);
            signal?.removeEventListener('abort', abort);
            controller.abort();
        }
    }
}
/** One server process/context, scoped to this client tab. Never silently restarted. */
export class BridgeSession {
    client;
    options;
    conversationId;
    opening;
    pendingJob;
    closed = false;
    submitting = false;
    uncertainKey;
    releaseHandlers = () => { this.closed = true; this.options.tools = undefined; };
    constructor(client, options) {
        this.client = client;
        this.options = options;
        if (options.id && (options.group || options.provider || options.action || options.mode || options.tools !== undefined))
            throw new BridgeError('invalid_session', 'A resumed session takes an id without a new target selection or tools.', 400);
        this.options = { ...options, tools: captureTools(options.tools, options.mode) };
        this.conversationId = options.id;
    }
    get id() { return this.conversationId; }
    async send(input) {
        if (this.closed)
            throw new BridgeError('session_closed', 'This session has ended. Create a new session explicitly.', 410);
        if (this.submitting)
            throw new BridgeError('session_busy', 'Wait for the current message to finish.', 409);
        const message = typeof input === 'string' ? { prompt: input } : input;
        if ('tools' in message)
            throw validation('invalid_tools', 'Session tools cannot change. Create a new session and supply saved history.');
        if (this.uncertainKey && message.idempotencyKey !== this.uncertainKey) {
            const error = new BridgeError('submission_unknown', 'Resolve the previous submission with recover(), or retry its exact input and idempotency key.', 0, { category: 'transport' });
            error.idempotencyKey = this.uncertainKey;
            throw error;
        }
        this.submitting = true;
        try {
            if (this.conversationId && !this.client.connected)
                throw new BridgeError('session_closed', 'This tab connection ended. Create a new session explicitly.', 410);
            if (this.conversationId && !this.opening)
                await this.client.attachSession(this.conversationId, this.options);
            if (!this.conversationId) {
                if (!this.opening)
                    this.opening = this.client.openSession(this.options).then(conversation => { this.conversationId = conversation.id; });
                try {
                    await this.opening;
                }
                finally {
                    this.opening = undefined;
                }
            }
            if (this.closed)
                throw new BridgeError('session_closed', 'This session was closed while it was opening.', 410);
            if (this.pendingJob) {
                const previous = await this.client.get(this.pendingJob);
                if (!done(previous))
                    throw new BridgeError('session_busy', 'Wait for the current message to finish.', 409);
                this.pendingJob = undefined;
            }
            const job = await this.client.sendSession(this.conversationId, message);
            this.pendingJob = job.id;
            this.uncertainKey = undefined;
            return job;
        }
        catch (error) {
            if (error instanceof BridgeError && error.idempotencyKey)
                this.uncertainKey = error.idempotencyKey;
            throw error;
        }
        finally {
            this.submitting = false;
        }
    }
    async *stream(input, signal) {
        if (signal?.aborted)
            throw signal.reason ?? new DOMException('Aborted', 'AbortError');
        const job = await this.send(input);
        for await (const update of this.client.stream(job.id, signal)) {
            if (done(update))
                this.pendingJob = undefined;
            yield update;
        }
    }
    async run(input, signal) {
        let result;
        for await (const update of this.stream(input, signal))
            result = update;
        if (!result || result.status !== 'completed') {
            if (result)
                throw new BridgeJobError(result);
            throw new BridgeError('session_closed', 'The session ended without a result.', 410);
        }
        return result;
    }
    async recover() { if (!this.uncertainKey)
        return this.pendingJob ? this.client.get(this.pendingJob) : null; const job = await this.client.lookup(this.uncertainKey); if (job) {
        this.pendingJob = done(job) ? undefined : job.id;
        this.uncertainKey = undefined;
    } return job; }
    async close() {
        if (this.closed)
            return;
        this.releaseHandlers();
        if (this.opening)
            await this.opening.catch(() => { });
        if (this.conversationId && this.client.connected)
            await this.client.closeSession(this.conversationId);
    }
}
