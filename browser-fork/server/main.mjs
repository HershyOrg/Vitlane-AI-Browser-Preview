import { createBridge } from './app.mjs';
import { createProvider } from './provider.mjs';

try {
  const provider = createProvider({
    baseUrl: process.env.MODEL_BASE_URL || 'https://api.openai.com/v1',
    // Both variables stay server-side. MODEL_API_KEY permits a dedicated
    // browser credential; the managed monorepo secret is the fallback.
    apiKey: process.env.MODEL_API_KEY || process.env.MANAGED_OPENAI_API_SECRET,
    model: process.env.MODEL_NAME,
  });
  const host = process.env.HOST || '127.0.0.1';
  const port = Number(process.env.PORT || 8787);
  if (!Number.isInteger(port) || port < 1 || port > 65535) throw new Error('Invalid PORT');
  if (!['127.0.0.1', '::1', 'localhost'].includes(host)) {
    throw new Error('Use a local TLS reverse proxy for remote access; HOST must be loopback');
  }
  // BRIDGE_TOKEN is intentionally separate from the model credential and is
  // also the shared v1 command-permit HMAC key for the paired native client.
  const server = createBridge({ token: process.env.BRIDGE_TOKEN, provider });
  server.on('error', () => { console.error('Bridge could not listen on the configured address'); process.exitCode = 1; });
  server.listen(port, host, () => console.log(`Vitlane AI browser bridge listening at http://${host}:${port}`));
  for (const signal of ['SIGTERM', 'SIGINT']) process.on(signal, () => server.close());
} catch (e) {
  console.error(e.message);
  process.exitCode = 1;
}
