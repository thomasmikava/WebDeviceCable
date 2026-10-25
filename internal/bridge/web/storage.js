import { BridgeError, checkOrigin } from './secure.js';
/** Browser-local, account-scoped persistence. CryptoKeys stay non-extractable.
 * Keep one client/lease per tab; only the pairing is shared between tabs. */
export class ConnectionStore {
    scope;
    constructor(account) {
        const origin = account.origin ?? globalThis.location?.origin;
        if (!origin || !account.appId || !account.appUser)
            throw new BridgeError('identity_required', 'An app ID, signed-in account and origin are required.', 400);
        checkOrigin(origin);
        this.scope = JSON.stringify([origin, account.appId, account.appUser]);
    }
    async transaction(mode, operation) {
        if (typeof indexedDB === 'undefined')
            throw new BridgeError('storage_unavailable', 'This browser cannot save connections. Keep the connection in memory for this visit.', 0, { category: 'unavailable' });
        return new Promise((resolve, reject) => {
            const failure = () => new BridgeError('storage_unavailable', 'The browser could not save or read this connection. Allow site storage or reconnect for this visit.', 0, { category: 'unavailable' });
            let settled = false, db, transaction;
            const fail = () => {
                if (settled)
                    return;
                settled = true;
                clearTimeout(deadline);
                try {
                    transaction?.abort();
                }
                catch { /* Already completed or aborted. */ }
                db?.close();
                reject(failure());
            };
            const deadline = setTimeout(fail, 5000);
            let open;
            try {
                open = indexedDB.open('webdevicecable-connections', 1);
            }
            catch {
                fail();
                return;
            }
            open.onupgradeneeded = () => { open.result.createObjectStore('connections'); };
            open.onerror = fail;
            open.onblocked = fail;
            open.onsuccess = () => {
                db = open.result;
                if (settled) {
                    db.close();
                    return;
                }
                db.onversionchange = () => db?.close();
                try {
                    transaction = db.transaction('connections', mode);
                    const request = operation(transaction.objectStore('connections'));
                    transaction.oncomplete = () => { db?.close(); if (settled)
                        return; settled = true; clearTimeout(deadline); resolve(request.result); };
                    transaction.onabort = transaction.onerror = fail;
                }
                catch {
                    fail();
                }
            };
        });
    }
    async load() {
        const connection = await this.transaction('readonly', store => store.get(this.scope));
        if (connection && (!connection.privateKey || connection.privateKey.extractable || connection.privateKey.type !== 'private' || connection.privateKey.algorithm.name !== 'ECDSA' || !/^[a-f0-9]{64}$/.test(connection.pairingId)))
            throw new BridgeError('invalid_connection', 'The saved connection is invalid. Forget it and pair again.', 0, { category: 'security' });
        return connection;
    }
    async save(connection) {
        if (!connection.privateKey || connection.privateKey.extractable)
            throw new BridgeError('invalid_connection', 'Save only a non-extractable pairing key.', 400);
        await this.transaction('readwrite', store => store.put(connection, this.scope));
    }
    async clear() { await this.transaction('readwrite', store => store.delete(this.scope)); }
}
