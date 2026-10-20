import { test } from 'node:test'
import assert from 'node:assert/strict'
import { layered, languageList, problems, report, flatten, isPlural, pluralCategories, pluralLocale, displayName } from './langpack.js'
import { LANGUAGES, pick } from './locale.js'

const en = {
  'a.hello': 'Hello',
  'a.bye': 'Bye {name}',
  'a.only': 'Only English',
  'a.n': { one: '{count} file', other: '{count} files' },
}

test('layers: personal over server over built-in over English', () => {
  const builtin = { 'a.hello': 'Ciao', 'a.bye': 'Ciao {name}' }
  const server = { 'a.bye': 'Arrivederci {name}', 'a.n': { one: '{count} file', other: '{count} file' } }
  const personal = { 'a.hello': 'Ehi' }
  const m = layered(en, builtin, server, personal)
  assert.equal(m['a.hello'], 'Ehi')
  assert.equal(m['a.bye'], 'Arrivederci {name}')
  assert.equal(m['a.only'], 'Only English')
  assert.deepEqual(m['a.n'], { one: '{count} file', other: '{count} file' })
  // missing layers are skipped
  assert.equal(layered(en, null, undefined, { 'a.only': 'x' })['a.only'], 'x')
  assert.equal(layered(en, {}, null, null)['a.hello'], 'Hello')
})

test('languages on offer', () => {
  const list = languageList(LANGUAGES, [{ code: 'pl', name: 'Polski' }, { code: 'it', name: 'Italiano (ufficio)' }], { code: 'xx', name: 'Mine' })
  assert.deepEqual(list.map((l) => l.code), ['en', 'it', 'es', 'tr', 'de', 'id', 'ja', 'pl', 'xx'])
  assert.equal(list.find((l) => l.code === 'it').name, 'Italiano (ufficio)')
  assert.equal(LANGUAGES.find((l) => l.code === 'it').name, 'Italiano') // not changed in place
  // the personal pack's name wins; a pack without one keeps the name
  assert.equal(languageList(LANGUAGES, [{ code: 'pl', name: 'Polski' }], { code: 'pl', name: 'Mój polski' }).filter((l) => l.code === 'pl').length, 1)
  assert.equal(languageList(LANGUAGES, [], { code: 'es', name: '' }).find((l) => l.code === 'es').name, 'Español')
})

test('pick with language packs', () => {
  const codes = ['en', 'it', 'es', 'tr', 'pl', 'pt-BR']
  assert.equal(pick('pl', [], codes), 'pl')
  assert.equal(pick('pl', ['it']), 'it') // without the packs, pl isn't on offer
  assert.equal(pick('', ['pl-PL', 'it'], codes), 'pl')
  assert.equal(pick('', ['pt-br'], codes), 'pt-BR')
  assert.equal(pick('', ['fr'], codes), 'en')
})

test('plural rules', () => {
  assert.deepEqual(pluralCategories('ru'), ['one', 'few', 'many', 'other'])
  assert.deepEqual(pluralCategories('ja'), ['other'])
  assert.equal(pluralLocale('de'), 'de')
  assert.equal(pluralLocale('qqq'), 'en')
  assert.equal(pluralLocale('not a code'), 'en')
  assert.equal(displayName('de'), 'Deutsch')
  assert.equal(displayName('qqq'), '')
})

test('flatten: nested keys, plurals, i18next suffixes', () => {
  const bad = []
  const out = flatten({ a: { hello: 'Hallo', n_one: '{count} Datei', n_other: '{count} Dateien', deep: { x: 'y' } }, 'a.bye': 'Tschüss {name}', b: 3 }, en, bad)
  assert.deepEqual(out, { 'a.hello': 'Hallo', 'a.n': { one: '{count} Datei', other: '{count} Dateien' }, 'a.deep.x': 'y', 'a.bye': 'Tschüss {name}' })
  assert.deepEqual(bad, ['b'])
  assert.ok(isPlural({ one: 'a', other: 'b' }))
  assert.ok(!isPlural({ hello: 'a' }))
  assert.ok(!isPlural({}))
})

test('problems: the server rules', () => {
  const ok = { code: 'de', name: 'Deutsch', messages: { 'a.hello': 'Hallo', 'a.n': { one: 'x', other: 'y' } } }
  assert.deepEqual(problems(ok), [])
  assert.deepEqual(problems({ ...ok, code: 'DE' }).map((p) => p.kind), ['code'])
  assert.deepEqual(problems({ ...ok, code: 'pt-BR' }), [])
  assert.deepEqual(problems({ ...ok, name: '' }).map((p) => p.kind), ['name'])
  assert.deepEqual(problems({ ...ok, name: 'x'.repeat(41) }).map((p) => p.kind), ['name'])
  const kinds = (messages) => problems({ ...ok, messages }).map((p) => `${p.kind} ${p.key}`)
  assert.deepEqual(kinds({ 'bad key': 'x' }), ['key bad key'])
  assert.deepEqual(kinds({ 'a.b': 'x'.repeat(2001) }), ['long a.b'])
  assert.deepEqual(kinds({ 'a.b': { one: 'x' } }), ['other a.b'])
  assert.deepEqual(kinds({ 'a.b': { lots: 'x', other: 'y' } }), ['form a.b'])
  assert.deepEqual(kinds({ 'a.b': 3 }), ['form a.b'])
  assert.deepEqual(kinds({ 'a.b': { other: 'y'.repeat(2001) } }), ['long a.b'])
  const big = {}
  for (let i = 0; i < 300; i++) big[`k.${i}`] = 'x'.repeat(1900)
  assert.deepEqual(problems({ ...ok, messages: big }).map((p) => p.kind), ['size'])
})

test('report: unknown keys, placeholders, untranslated', () => {
  const r = report({ code: 'de', name: 'Deutsch', messages: { 'a.hello': 'Hallo', 'a.bye': 'Tschüss {nome}', 'a.extra': 'x', 'a.n': { one: 'eine Datei', other: '{count} Dateien' } } }, en)
  assert.deepEqual(r.unknown, ['a.extra'])
  assert.deepEqual(r.errors.map((e) => `${e.kind} ${e.key} ${e.want}`), ['placeholders a.bye {name}'])
  assert.equal(r.untranslated, 1) // a.only
  assert.equal(r.total, 4)
  assert.equal(r.count, 4)
  // a plural's placeholders are all its forms'
  assert.deepEqual(report({ code: 'de', name: 'D', messages: { 'a.n': { other: '{count} Dateien' } } }, en).errors, [])
})
