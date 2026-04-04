import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import tailwindcss from '@tailwindcss/vite';
import path from 'path';

export default defineConfig({
  test: {
    include: ['src/**/*.test.ts'],
  },
  plugins: [
    tailwindcss(),
    svelte(),
  ],
  resolve: {
    alias: {
      '$lib': path.resolve('./src/lib'),
    },
  },
  build: {
    outDir: '../cmd/server/frontend/dist',
    emptyOutDir: true,
    chunkSizeWarningLimit: 800, // main chunk ~780KB after splitting monaco/xterm; workers are larger but async
    rollupOptions: {
      output: {
        manualChunks: {
          'monaco': ['monaco-editor', '@monaco-editor/loader'],
          'xterm': ['@xterm/xterm', '@xterm/addon-fit', '@xterm/addon-web-links'],
        },
      },
    },
  },
  server: {
    proxy: {
      '/api': {
        target: `http://localhost:${process.env.PORT || 9770}`,
        ws: true,
      },
    },
  },
});
