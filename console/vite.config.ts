import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'node:path'

// The SPA is embedded in the hub Go binary and served under /console/.
// `base` must match that mount path so hashed asset URLs resolve.
// `emptyOutDir:false` preserves the committed dist/index.html placeholder +
// dist/.gitignore (which keep `go:embed all:dist` compiling before a build).
export default defineConfig({
  base: '/console/',
  plugins: [react()],
  resolve: {
    alias: { '@': path.resolve(__dirname, 'src') },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: false,
    sourcemap: false,
  },
  server: {
    port: 5199,
    // Dev proxy so `pnpm dev` talks to a running hub without CORS.
    proxy: {
      '/hugr': 'http://localhost:15000',
      '/api': 'http://localhost:15000',
      '/skills': 'http://localhost:15000',
    },
  },
})
