// This example can only use the explicitly configured development runner.
export function exampleEndpoint(raw) {
  if (!raw) throw new Error('Set BRIDGE_URL to your isolated development runner.');
  const url = new URL(raw);
  if (url.protocol !== 'http:' || url.hostname !== '127.0.0.1' || !url.port || url.port === '47321' || url.username || url.password || url.search || url.hash || url.pathname !== '/') throw new Error('BRIDGE_URL must be an isolated loopback HTTP origin, away from the installed WDC port.');
  return url.origin;
}

export function assertExampleConnection(connection, endpoint) {
  if (connection?.identity?.endpoint !== endpoint) throw new Error('Use a connection from the configured isolated development runner.');
}
