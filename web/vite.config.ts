import { fileURLToPath, URL } from 'node:url'

import tailwindcss from '@tailwindcss/vite'
import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'

// Multi-page build: the console is the site root and the TODO demo app is a
// second Vite entry under /apps/todo/, matching the route embed.go serves
// (spec/features/local-server-and-web-console#REQ:route-layout). Vite keeps
// each HTML entry's own output path relative to the project root, so
// apps/todo/index.html here becomes dist/apps/todo/index.html.
export default defineConfig({
  plugins: [vue(), tailwindcss()],
  server: {
    fs: {
      // copy/en.json lives one level above web/ (repo root), and src/copy.ts
      // imports it directly so both Go and Vite read the same catalogue file
      // (spec/features/configuration-parity#REQ:copy-catalogue). The dev
      // server otherwise refuses to serve files outside its project root.
      allow: ['..'],
    },
  },
  build: {
    rollupOptions: {
      input: {
        console: fileURLToPath(new URL('./index.html', import.meta.url)),
        todo: fileURLToPath(new URL('./apps/todo/index.html', import.meta.url)),
      },
    },
  },
})
