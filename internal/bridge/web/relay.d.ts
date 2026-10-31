/** Backend-only SDK. The project relay verifies the desktop's protocol-v2 frames. */
import { BridgeClient } from './index.js';
import type { ActionGroups, AppTool } from './index.js';
export { BridgeError, BridgeJobError } from './index.js';
export type * from './index.js';
export interface RelayOptions {
    baseURL: string;
    apiKey: string;
    connectionId: string;
    account: {
        appId: string;
        appUser: string;
    };
    origin: string;
    groups?: ActionGroups;
    tools?: AppTool[];
    /** Recheck project authorization before every operation, including heartbeats/tools. */
    authorize?: () => Promise<void>;
}
export interface RelayEnrollment {
    code: string;
    connectionId: string;
    expiresAt: string;
    appId: string;
    name: string;
    origin: string;
}
export interface RelayStatus {
    connectionId: string;
    appUser: string;
    connected: boolean;
    revoked: boolean;
}
export interface RelayProject {
    id: string;
    appId: string;
    name: string;
    origin: string;
    createdAt: string;
    revoked: boolean;
}
/** Returned once on creation or rotation. Store apiKey as a backend secret. */
export interface RelayProjectCredential {
    project: RelayProject;
    apiKey: string;
}
export interface RelayAdminOptions {
    baseURL: string;
    adminKey: string;
}
export declare function relayRequest<T>(options: Pick<RelayOptions, 'baseURL' | 'apiKey'>, path: string, init?: RequestInit): Promise<T>;
/** Register every project once, including a relay hosting just one website.
 * Operator-only: never give this credential to hosted projects or browsers. */
export declare class RelayAdminClient {
    private readonly options;
    constructor(options: RelayAdminOptions);
    createProject(identity: Pick<RelayProject, 'appId' | 'name' | 'origin'>): Promise<RelayProjectCredential>;
    listProjects(): Promise<RelayProject[]>;
    getProject(options: {
        projectId: string;
    }): Promise<RelayProject>;
    rotateProjectKey(options: {
        projectId: string;
    }): Promise<RelayProjectCredential>;
    /** Irreversible: cancels connectors and requires a new app ID and fresh desktop consent. */
    revokeProject(options: {
        projectId: string;
    }): Promise<RelayProject>;
    private projectPath;
}
export declare class RelayClient extends BridgeClient {
    readonly relay: RelayOptions;
    constructor(relay: RelayOptions);
    status(): Promise<RelayStatus>;
    static enroll(options: Pick<RelayOptions, 'baseURL' | 'apiKey'> & {
        appUser: string;
    }): Promise<RelayEnrollment>;
    static revoke(options: Pick<RelayOptions, 'baseURL' | 'apiKey' | 'connectionId'> & {
        appUser: string;
    }): Promise<{
        revoked: boolean;
    }>;
}
