import { defineConfig } from 'vite';

// The LG A1 (webOS 6.0) browser is Chromium ~79: esbuild lowers ?. ?? etc.
export default defineConfig({
  esbuild: { jsx: 'automatic', jsxImportSource: 'preact' },
  build: {
    target: ['chrome79', 'safari13'],
    outDir: 'dist',
    emptyOutDir: false,
    assetsDir: 'assets',
    sourcemap: false,
    modulePreload: { polyfill: false },
    chunkSizeWarningLimit: 700,
  },
  server: {
    proxy: { '/api': 'http://127.0.0.1:8420' },
  },
});
