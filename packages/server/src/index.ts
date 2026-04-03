import Fastify from 'fastify';
import fastifyWebSocket from '@fastify/websocket';
import fastifyCors from '@fastify/cors';
import fastifyStatic from '@fastify/static';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { initDb } from './db/index.js';
import { handleConnection } from './ws/handler.js';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

const PORT = parseInt(process.env['PORT'] ?? '3000', 10);
const PROJECT_PATH = process.env['PROJECT_PATH'] ?? process.cwd();

async function main(): Promise<void> {
  initDb(PROJECT_PATH);
  console.log(`[myeditor] Project: ${PROJECT_PATH}`);

  const app = Fastify({ logger: { level: 'info' } });

  await app.register(fastifyCors, { origin: true });
  await app.register(fastifyWebSocket);

  // In production, serve the built client
  const clientDist = path.join(__dirname, '../../client/dist');
  try {
    await app.register(fastifyStatic, {
      root: clientDist,
      prefix: '/',
    });
  } catch {
    // Client not built yet — fine in dev
  }

  // WebSocket endpoint
  app.get('/ws', { websocket: true }, (socket, _req) => {
    handleConnection(socket, PROJECT_PATH);
  });

  // Health check
  app.get('/health', async () => ({ status: 'ok', projectPath: PROJECT_PATH }));

  await app.listen({ port: PORT, host: '0.0.0.0' });
  console.log(`[myeditor] Server running at http://localhost:${PORT}`);
  console.log(`[myeditor] WebSocket at ws://localhost:${PORT}/ws`);
  console.log(`[myeditor] Open http://localhost:5173 in dev mode`);
}

main().catch(err => {
  console.error('[myeditor] Fatal startup error:', err);
  process.exit(1);
});
