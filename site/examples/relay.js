import { RelayClient, relayConnectURL } from '@webdevicecable/relay';

const project = { baseURL: process.env.WDC_RELAY_URL, apiKey: process.env.WDC_PROJECT_KEY };
const appUser = String(signedInUser.id);

// Once per user: show them this link. They approve your site in WDC.
const enrollment = await RelayClient.enroll({ ...project, appUser });
const link = relayConnectURL({ baseURL: project.baseURL, code: enrollment.code });
await db.saveConnection(appUser, enrollment);

// Any time later, even while your website is closed:
const saved = await db.getConnection(appUser);
const client = new RelayClient({
  ...project, connectionId: saved.connectionId, origin: saved.origin,
  account: { appId: saved.appId, appUser },
  groups: [{ id: 'chat', name: 'Chat', kind: 'ai' }],
});
await client.connect();
const job = await client.run({ group: 'chat', prompt: 'Hello' });
console.log(job.output);
await client.close();
