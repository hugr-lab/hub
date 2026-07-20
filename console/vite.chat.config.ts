import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'node:path'

// Separable chat microfrontend build. Produces a self-contained bundle
// (React bundled in) exposing `mountChat` / `<hub-chat>` for embedding in a
// JupyterLab Lumino Widget or any host. Output → dist-chat/.
export default defineConfig({
  plugins: [react()],
  resolve: { alias: { '@': path.resolve(__dirname, 'src') } },
  define: { 'import.meta.env.BASE_URL': JSON.stringify('/console/') },
  build: {
    outDir: 'dist-chat',
    emptyOutDir: true,
    sourcemap: false,
    copyPublicDir: false, // don't copy the SPA's public/ into the lib bundle
    lib: {
      entry: path.resolve(__dirname, 'src/chat/index.ts'),
      name: 'HubChat',
      formats: ['es', 'umd'],
      fileName: (fmt) => `hub-chat.${fmt}.js`,
      cssFileName: 'hub-chat',
    },
  },
})
