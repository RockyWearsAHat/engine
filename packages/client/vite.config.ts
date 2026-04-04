import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      '/ws': { target: 'ws://localhost:3000', ws: true },
    },
  },
  optimizeDeps: {
    include: ['@xterm/xterm', '@xterm/addon-fit', '@xterm/addon-web-links'],
    exclude: ['@engine/shared'],
  },
});
