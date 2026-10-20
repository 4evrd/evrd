// Text is never rendered as HTML: messages can come from uploaded language
// packs. The one {@html} left draws the app's own icons.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

const src = fileURLToPath(new URL('../', import.meta.url))
const files = (dir) => readdirSync(dir, { withFileTypes: true }).flatMap((e) => (e.isDirectory() ? files(join(dir, e.name)) : e.name.endsWith('.svelte') ? [join(dir, e.name)] : []))

test('no {@html} but the icons', () => {
  const uses = files(src).filter((f) => readFileSync(f, 'utf8').includes('{@html')).map((f) => f.slice(src.length))
  assert.deepEqual(uses, ['lib/components/Icon.svelte'])
  assert.match(readFileSync(join(src, 'lib/components/Icon.svelte'), 'utf8'), /\{@html ICONS\[name\] \|\| ''\}/)
})
