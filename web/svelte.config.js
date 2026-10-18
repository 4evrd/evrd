import adapter from '@sveltejs/adapter-static'
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte'

/** @type {import('@sveltejs/kit').Config} */
export default {
  preprocess: vitePreprocess(),
  kit: {
    // Static SPA: a single index.html fallback, all routing client-side.
    // nginx (or any static server) serves ./build and proxies /api + /auth to
    // the daemon, so the app and its API share an origin (cookie sessions).
    adapter: adapter({ fallback: 'index.html', strict: false }),
  },
}
