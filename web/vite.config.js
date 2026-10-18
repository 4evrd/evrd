import { sveltekit } from '@sveltejs/kit/vite'
import { defineConfig } from 'vite'

// Dev: proxy the API to the daemon so the app and API share an origin, the
// same way nginx does in production. EVRD_API points at the daemon.
const api = process.env.EVRD_API || 'http://localhost:8098'

export default defineConfig({
  plugins: [sveltekit()],
  server: {
    host: true,
    port: 5173,
    proxy: {
      '/api': { target: api, ws: true }, // ws: the live message stream
      '/auth': api,
    },
  },
})
