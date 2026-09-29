/// <reference types="vitest/config" />
import { defineConfig, type Plugin } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { svelteGrab } from 'point-to-svelte/vite';
import { writeFileSync, mkdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const hub = process.env.YIP_HUB ?? 'http://127.0.0.1:7521';

// The Go hub embeds web/dist; git tracks web/dist/.gitkeep so the embed
// directive always has a directory. emptyOutDir removes it, so restore it.
function keepGitkeep(): Plugin {
  return {
    name: 'yip-keep-gitkeep',
    apply: 'build',
    closeBundle() {
      const dist = fileURLToPath(new URL('./dist/', import.meta.url));
      mkdirSync(dist, { recursive: true });
      writeFileSync(dist + '.gitkeep', '');
    },
  };
}

export default defineConfig({
  // point-to-svelte's plugin applies only to `vite dev`, where it serves the
  // overlay's open-in-editor requests; main.ts loads the overlay itself. Its
  // HTML injection is off because 0.2.0 injects an inline bare import that
  // Vite doesn't resolve.
  plugins: [svelte(), svelteGrab({ inject: false }), keepGitkeep()],
  // Component tests mount the client runtime of Svelte, not the server one.
  resolve: process.env.VITEST ? { conditions: ['browser'] } : undefined,
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    assetsDir: 'assets',
    target: 'es2022',
    sourcemap: false,
    // Astryx with its i18n runtime is ~620 kB of the bundle (yip's own code
    // is ~390 kB), and it's all used; warn only if that grows meaningfully.
    chunkSizeWarningLimit: 1200,
  },
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      // Only APIs: Vite still serves the client at /w/<id>/... in development.
      '^/w/[^/]+/v1(?:/|$)': {
        target: hub,
        changeOrigin: false,
      },
      '/v1': {
        target: hub,
        changeOrigin: false,
        // Server-sent events stream through unbuffered (the hub sets
        // X-Accel-Buffering: no and Cache-Control: no-store).
      },
    },
  },
  test: {
    environment: 'jsdom',
    include: ['tests/unit/**/*.test.ts'],
    setupFiles: ['tests/unit/setup.ts'],
  },
});
