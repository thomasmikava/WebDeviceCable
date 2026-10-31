import { BridgeError } from './secure.js';
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
    outputSchema?: Record<string, unknown>;
    inputs?: string[];
}
export interface Job {
    id: string;
    pairingId: string;
    status: JobStatus;
    code?: string;
    detail?: string;
    output?: string;
    actualProvider?: Provider;
    actualModel?: string;
    billing: 'none' | 'native_subscription';
    revision: number;
    request: JobRequest;
    createdAt: string;
    finishedAt?: string;
    actionTitle?: string;
    exitCode?: number;
    outputTruncated?: boolean;
    execution?: ExecutionMetadata;
    error?: Failure;
    turn?: number;
    json?: unknown;
    artifacts?: Artifact[];
    usage?: TokenUsage;
    inputNames?: string[];
}
export interface Artifact {
    id: string;
    name: string;
    mediaType: string;
    size: number;
    sha256: string;
}
export interface TokenUsage {
    inputTokens: number;
    cachedInputTokens: number;
    outputTokens: number;
    reasoningOutputTokens: number;
    totalTokens: number;
}
export interface UploadedInput extends Artifact {
}
export interface AdmissionStatus {
    available: boolean;
    kind?: 'native' | 'command';
    code?: string;
}
export interface Pairing {
    id: string;
    appId: string;
    appUser: string;
    name: string;
    origin: string;
    deviceId: string;
    automatic: boolean;
    paused: boolean;
    revoked: boolean;
    mode: 'restricted' | 'commands';
    actions?: Record<string, string>;
}
export interface Capability {
    id: Provider;
    name: string;
    execution: boolean;
    eligibility: string;
    reason: string;
    source: string;
    appTools: boolean;
    workspace: boolean;
    text: boolean;
    json: boolean;
    imageInput: boolean;
    documentInput: boolean;
    imageGeneration: boolean;
    imageEditing: boolean;
}
export interface ActionArgument {
    name: string;
    title?: string;
    type?: string;
    default?: string;
    rejectNull?: boolean;
    choices?: {
        title?: string;
        value: string;
    }[];
}
export interface CommandAction {
    id: string;
    title?: string;
    arguments: ActionArgument[] | null;
    timeout: number;
    available: boolean;
    sessions: boolean;
}
export interface Capabilities {
    appToolPermissions?: 'application';
    fileTransfer: boolean;
    maxTextBytes: number;
    protocolVersion: 2;
    pairing: Pairing;
    providers: Capability[];
    actions: CommandAction[];
    connectionTest: boolean;
    leaseSeconds: number;
    modes: string[];
    remoteAccess: false;
}
export interface ExecutionMetadata {
    kind: 'command' | 'native' | 'connection-test';
    name?: string;
    version?: string;
    versionSource?: 'configured' | 'native';
    provider?: Provider;
    model?: string;
    reasoning?: string;
}
export interface ActionGroup {
    id: string;
    name: string;
    kind?: 'ai' | 'command';
    parentId?: string;
}
export interface GroupDefinition extends ActionGroup {
    children?: GroupDefinition[];
}
export type ActionGroups = GroupDefinition[] | Record<string, string>;
export interface Conversation {
    id: string;
    group?: string;
    status: 'open' | 'closed';
    code?: string;
    lastJobId?: string;
    turns: number;
    createdAt: string;
    updatedAt: string;
}
export interface SessionOptions {
    mode?: "restricted" | "app-tools" | "workspace";
    group?: string;
    provider?: Provider;
    action?: string;
    id?: string;
}
export interface MessageOptions {
    outputSchema?: Record<string, unknown>;
    inputs?: string[];
    prompt?: string;
    arguments?: Record<string, string>;
    timeoutSeconds?: number;
    idempotencyKey?: string;
}
export type RunOptions = Omit<JobRequest, 'idempotencyKey' | 'kind'> & {
    kind?: JobRequest['kind'];
    idempotencyKey?: string;
};
export interface AppToolDefinition {
    id: string;
    title: string;
    description: string;
    inputSchema: Record<string, unknown>;
    readOnly?: boolean;
}
export interface AppTool extends AppToolDefinition {
    execute: (args: Record<string, unknown>, context: {
        idempotencyKey: string;
        signal: AbortSignal;
    }) => unknown | Promise<unknown>;
}
/** App tool metadata is descriptive; its app owns authorization. Legacy grants is always empty. */
export interface PermissionStatus {
    tools: Record<string, AppToolDefinition & {
        hash: string;
    }>;
    grants: Record<string, {
        hash: string;
        automatic: boolean;
    }>;
    folders: {
        id: string;
        name: string;
        write: boolean;
        automatic: boolean;
    }[];
}
export interface ClientOptions {
    tools?: AppTool[];
    baseURL?: string;
    connection: ConnectionKeys;
    origin?: string;
    groups?: ActionGroups;
    account?: {
        appId: string;
        appUser: string;
    };
}
export interface BridgeTransport {
    reset(): void;
    fetch(path: string, init?: RequestInit, lease?: string, invitation?: string): Promise<Response>;
    close(): Promise<void>;
}
export declare class BridgeJobError extends BridgeError {
    job: Job;
    constructor(job: Job);
}
export declare class BridgeClient {
    private options;
    private transport;
    private toolHandlers;
    private toolsBusy;
    private toolsTimer?;
    private lifetime;
    private generation;
    private lease;
    private leaseExpiresAt;
    private connecting?;
    private timer?;
    private heartbeatPending;
    private closed;
    private watchers;
    private onPageHide;
    constructor(options: ClientOptions, transport?: BridgeTransport);
    static pair(options: {
        baseURL?: string;
        invitation: string;
        appId: string;
        appUser: string;
        origin?: string;
    }): Promise<{
        connection: ConnectionKeys;
        pairing: Pairing;
    }>;
    private static pairWithKeys;
    /** Call directly from a click handler. Approval happens in the authenticated local panel. */
    static requestPairing(options: {
        appId: string;
        name: string;
        appUser: string;
        baseURL?: string;
        timeoutMs?: number;
        launchInstalled?: boolean;
    }): Promise<{
        connection: ConnectionKeys;
        pairing: Pairing;
    }>;
    private static launchPairing;
    private request;
    get connected(): boolean;
    registerTools(tools: AppTool[]): Promise<void>;
    permissions(): Promise<PermissionStatus>;
    permissionRequest(id: string): Promise<{
        id: string;
        status: string;
    }>;
    requestPermissions(request: {
        tools?: string[];
        workspace?: boolean;
    }, options?: {
        wait?: boolean;
        signal?: AbortSignal;
    }): Promise<{
        id: string;
        status: string;
    }>;
    private drainTools;
    registerGroups(groups: ActionGroups): Promise<{
        groups: ActionGroup[];
    }>;
    groups(): Promise<{
        groups: ActionGroup[];
    }>;
    session(options?: SessionOptions): BridgeSession;
    run(input: RunOptions | string, signal?: AbortSignal): Promise<Job>;
    openSession(options: SessionOptions): Promise<Conversation>;
    sendSession(id: string, input: MessageOptions): Promise<Job>;
    getSession(id: string): Promise<Conversation>;
    closeSession(id: string): Promise<Conversation>;
    capabilities(): Promise<Capabilities>;
    preflight(request: Omit<JobRequest, 'idempotencyKey'>, features?: string[]): Promise<AdmissionStatus>;
    /** File bytes stay unchanged and scoped to this requesting tab. */
    upload(file: Blob, name?: string): Promise<UploadedInput>;
    removeInput(id: string): Promise<{
        ok: boolean;
    }>;
    /** Signed/encrypted chunks, checked again against the signed file hash. */
    artifact(job: Job, artifact?: Artifact): Promise<Blob>;
    actions(): Promise<{
        actions: CommandAction[];
    }>;
    /** One instance/lease per requesting tab. Do not share a lease between tabs. */
    connect(): Promise<void>;
    private openLease;
    private heartbeat;
    private stop;
    close(): Promise<void>;
    submit(request: JobRequest): Promise<Job>;
    lookup(idempotencyKey: string): Promise<Job | null>;
    get(id: string): Promise<Job>;
    cancel(id: string): Promise<Job>;
    /** Full job snapshots, reconnectable without replaying inference or app actions. */
    stream(id: string, signal?: AbortSignal): AsyncGenerator<Job>;
}
/** One server process/context, scoped to this client tab. Never silently restarted. */
export declare class BridgeSession {
    private client;
    private options;
    private conversationId?;
    private opening?;
    private pendingJob?;
    private closed;
    private submitting;
    private uncertainKey?;
    constructor(client: BridgeClient, options: SessionOptions);
    get id(): string | undefined;
    send(input: MessageOptions | string): Promise<Job>;
    stream(input: MessageOptions | string, signal?: AbortSignal): AsyncGenerator<Job>;
    run(input: MessageOptions | string, signal?: AbortSignal): Promise<Job>;
    recover(): Promise<Job | null>;
    close(): Promise<void>;
}
