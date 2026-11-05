/** Protocol v2: pinned identities, mutual signatures and ephemeral encrypted transport. */
export interface RunnerIdentity {
    deviceId: string;
    publicKey: string;
    endpoint: string;
}
export interface ConnectionKeys {
    pairingId: string;
    identity: RunnerIdentity;
    privateKey: CryptoKey;
    publicKey: string;
}
export interface Invitation extends RunnerIdentity {
    v: 2;
    secret: string;
    appId: string;
    appUser: string;
    origin: string;
    expiresAt: number;
}
export type ErrorCategory = 'validation' | 'authentication' | 'permission' | 'unavailable' | 'rate_limit' | 'context_limit' | 'cancelled' | 'timeout' | 'disconnected' | 'execution' | 'protocol' | 'conflict' | 'interrupted' | 'internal' | 'transport' | 'security';
export interface Failure {
    code: string;
    category: ErrorCategory;
    message: string;
    retryable: boolean;
    raw?: string;
}
export declare class BridgeError extends Error {
    code: string;
    status: number;
    readonly category: ErrorCategory;
    readonly retryable: boolean;
    readonly raw?: string;
    idempotencyKey?: string;
    constructor(code: string, message: string, status: number, details?: Partial<Failure>);
}
export declare class BridgeSecurityError extends BridgeError {
    constructor(message?: string);
}
export declare function base64url(bytes: Uint8Array): string;
export declare function unbase64url(text: string): Uint8Array<ArrayBuffer>;
export declare const nonce: () => string;
export declare function localURL(raw: string): string;
export declare function checkOrigin(origin: string): void;
export declare function parseInvitation(value: string): Invitation;
export declare function createSigningKeys(): Promise<{
    privateKey: CryptoKey;
    publicKey: string;
}>;
export declare class SecureTransport {
    private identity;
    private origin;
    private keys;
    private pairingId;
    readonly endpoint: string;
    /** Discard ephemeral state; the next handshake must still verify the original pin. */
    reset(): void;
    private channel?;
    private opening?;
    constructor(identity: RunnerIdentity, origin: string, keys: {
        privateKey: CryptoKey;
        publicKey: string;
    }, pairingId: string, endpoint?: string);
    private getChannel;
    close(): Promise<void>;
    private handshake;
    fetch(path: string, init?: RequestInit, lease?: string, invitation?: string): Promise<Response>;
    private fetchOnChannel;
    private frames;
    private events;
}
/** Signed launch rendezvous. The invitation itself is encrypted to an ephemeral key. */
export declare function launchRequest(endpoint: string, origin: string, keys: {
    privateKey: CryptoKey;
    publicKey: string;
}, details: {
    requestId: string;
    appId: string;
    name: string;
    appUser: string;
    logoURL?: string;
    requestedPermissions?: {
        automaticApprovals?: boolean;
    };
}, pinned?: RunnerIdentity): Promise<{
    identity: RunnerIdentity;
    status: string;
    invitation: string;
}>;
