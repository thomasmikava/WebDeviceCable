import type { ConnectionKeys } from './secure.js';
/** Browser-local, account-scoped persistence. CryptoKeys stay non-extractable.
 * Keep one client/lease per tab; only the pairing is shared between tabs. */
export declare class ConnectionStore {
    private scope;
    constructor(account: {
        appId: string;
        appUser: string;
        origin?: string;
    });
    private transaction;
    load(): Promise<ConnectionKeys | undefined>;
    save(connection: ConnectionKeys): Promise<void>;
    clear(): Promise<void>;
}
