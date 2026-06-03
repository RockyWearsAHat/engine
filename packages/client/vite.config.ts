import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

const webPort = Number(process.env.ENGINE_WEB_PORT ?? '24445');

export default defineConfig({
  plugins: [react()],
  server: {
    port: webPort,
    strictPort: true,
    proxy: {
      '/ws': { target: 'ws://127.0.0.1:24444', ws: true },
      '/health': { target: 'http://127.0.0.1:24444' },
    },
  },
  optimizeDeps: {
    include: ['@xterm/xterm', '@xterm/addon-fit', '@xterm/addon-web-links'],
    exclude: ['@engine/shared'],
  },
});
