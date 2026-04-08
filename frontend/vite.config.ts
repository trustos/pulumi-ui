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
    chunkSizeWarningLimit: 900, // main chunk ~850KB after adding xstate; monaco/xterm/xstate are split out
    rollupOptions: {
      // Suppress the LoopBlock circular-import warning — the dynamic import
      // is intentional (breaks LoopBlock ↔ ConditionalBlock cycle), not for code splitting.
      onwarn(warning, warn) {
        if (warning.code === 'CIRCULAR_DEPENDENCY') return;
        if (warning.message?.includes('LoopBlock.svelte')) return;
        warn(warning);
      },
      output: {
        manualChunks: {
          'monaco': ['monaco-editor', '@monaco-editor/loader'],
          'xterm': ['@xterm/xterm', '@xterm/addon-fit', '@xterm/addon-web-links'],
          'xstate': ['xstate', '@xstate/svelte'],
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
