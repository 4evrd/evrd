// Every language's message files against English: the same areas, no keys
// English lacks, the same {placeholders}, and plurals with an "other" form.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readdirSync } from 'node:fs'
import { LANGUAGES, pick } from '../locale.js'

const dir = new URL('./', import.meta.url)
const load = async (lang) => {
  const out = {}
  for (const f of readdirSync(new URL(lang + '/', dir)).filter((f) => f.endsWith('.js'))) {
    const m = (await import(new URL(`${lang}/${f}`, dir))).default
    for (const k of Object.keys(m)) {
      assert.ok(k.startsWith(f.replace(/\.js$/, '') + '.'), `${lang}/${f}: key ${k} should start with "${f.replace(/\.js$/, '')}."`)
      assert.ok(!(k in out), `${lang}: ${k} defined twice`)
    }
    Object.assign(out, m)
  }
  return out
}
const holes = (v) => [...new Set((typeof v === 'object' ? Object.values(v).join(' ') : v).match(/\{\w+\}/g) || [])].sort().join(' ')

test('message files', async () => {
  const en = await load('en')
  for (const [k, v] of Object.entries(en)) {
    if (typeof v === 'object') assert.ok('other' in v, `en ${k}: plural without "other"`)
    else assert.equal(typeof v, 'string', `en ${k}`)
  }
  for (const { code } of LANGUAGES.filter((l) => l.code !== 'en')) {
    const m = await load(code)
    for (const [k, v] of Object.entries(m)) {
      assert.ok(k in en, `${code}: ${k} isn't an English key`)
      assert.equal(typeof v, typeof en[k], `${code} ${k}: plural in one language only`)
      if (typeof v === 'object') assert.ok('other' in v, `${code} ${k}: plural without "other"`)
      assert.equal(holes(v), holes(en[k]), `${code} ${k}: placeholders differ from English`)
    }
  }
})

test('pick', () => {
  assert.equal(pick('it', ['en-US']), 'it')
  assert.equal(pick('', ['tr-TR', 'en']), 'tr')
  assert.equal(pick('', ['pl-PL', 'es-MX']), 'es')
  assert.equal(pick('', ['de-DE', 'es-MX']), 'de')
  assert.equal(pick('', ['pl']), 'en')
  assert.equal(pick('xx', ['it']), 'it')
})
