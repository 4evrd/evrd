// Moves SvelteKit's inline start-up script out of build/*.html into a file
// of its own (_app/immutable/boot.<hash>.js), so the app runs under a
// Content-Security-Policy that allows only same-origin scripts (no
// 'unsafe-inline') — like a reverse proxy that sets one policy for every
// site behind it. Runs after vite build (see package.json).
//
// It stays a classic script, not a module: SvelteKit's snippet finds where
// to mount through document.currentScript, which modules don't have.
import { createHash } from 'node:crypto'
import { readdirSync, readFileSync, writeFileSync, mkdirSync } from 'node:fs'
import { join } from 'node:path'

const build = new URL('../build/', import.meta.url).pathname
const outDir = join(build, '_app', 'immutable')
mkdirSync(outDir, { recursive: true })

let moved = 0
for (const name of readdirSync(build).filter((f) => f.endsWith('.html'))) {
  const path = join(build, name)
  const html = readFileSync(path, 'utf8')
  const out = html.replace(/<script>([\s\S]*?)<\/script>/g, (_, code) => {
    const body = code.trim() + '\n'
    const hash = createHash('sha256').update(body).digest('hex').slice(0, 10)
    const file = `boot.${hash}.js`
    writeFileSync(join(outDir, file), body)
    moved++
    return `<script src="/_app/immutable/${file}"></script>`
  })
  if (out !== html) writeFileSync(path, out)
}
console.log(`externalize-boot: moved ${moved} inline script(s)`)
