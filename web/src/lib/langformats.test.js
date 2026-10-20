import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import { FORMATS, FormatError, parseFile, exportFile, detect, codeFromName, normalizeCode, csvRows, poEntries, poPlurals, xml, yaml } from './langformats.js'

const en = {
  'a.hello': 'Hello',
  'a.bye': 'Bye {name}',
  'a.lines': 'Line one\nLine two',
  'a.tricky': 'Commas, "quotes", <tags> & \\ backslash: # not a comment',
  'a.n': { one: '{count} file', other: '{count} files' },
}
const de = {
  'a.hello': 'Hallo',
  'a.bye': 'Tschüss {name}',
  'a.lines': 'Zeile eins\nZeile zwei',
  'a.tricky': 'Kommas, „Zitate“, "quotes", <tags> & \\ Backslash: # kein Kommentar',
  'a.n': { one: '{count} Datei', other: '{count} Dateien' },
}
const fixture = (name) => readFileSync(new URL(`testdata/${name}`, import.meta.url), 'utf8')
const fails = (fn, kind) => assert.throws(fn, (e) => e instanceof FormatError && e.kind === kind)

// The real English messages, as the app bundles them.
const realEn = async () => {
  const dir = new URL('i18n/en/', import.meta.url)
  const out = {}
  for (const f of readdirSync(dir)) Object.assign(out, (await import(new URL(f, dir))).default)
  return out
}

test('every format round-trips, plurals included', () => {
  for (const f of FORMATS) {
    const file = exportFile(f.id, { code: 'de', name: 'Deutsch', messages: de, en })
    assert.equal(file.filename, `evrd-de.${f.ext}`)
    const back = parseFile(file.filename, file.text, en)
    assert.deepEqual(back.messages, de, f.id)
    assert.equal(back.code, 'de', f.id)
    assert.equal(back.name, 'Deutsch', f.id)
  }
})

test('round trips with the real English messages and other plural rules', async () => {
  const real = await realEn()
  const ru = { 'format.minutesAgo': { one: '{count} минуту назад', few: '{count} минуты назад', many: '{count} минут назад', other: '{count} минуты назад' }, 'account.language': 'Язык' }
  const ja = { 'format.minutesAgo': { other: '{count} 分前' } }
  for (const f of FORMATS) {
    for (const [code, messages] of [['ru', ru], ['ja', ja]]) {
      const file = exportFile(f.id, { code, name: 'x', messages, en: real })
      const back = parseFile(file.filename, file.text, real)
      // gettext has fewer forms than Intl for ru: other is the few form there
      const want = f.id === 'po' && code === 'ru' ? { ...ru, 'format.minutesAgo': { ...ru['format.minutesAgo'], other: '{count} минут назад' } } : messages
      assert.deepEqual(back.messages, want, `${f.id} ${code}`)
    }
  }
})

test('templates: every English key, nothing translated', async () => {
  const real = await realEn()
  for (const f of FORMATS) {
    const file = exportFile(f.id, { code: 'it', messages: { 'a.hello': 'Ciao' }, en: real, template: true })
    assert.equal(file.filename, `evrd-template.${f.templateExt || f.ext}`)
    for (const k of ['account.language', 'format.minutesAgo']) assert.ok(file.text.includes(k), `${f.id} has ${k}`)
    const back = parseFile(file.filename, file.text, real)
    assert.deepEqual(back.messages, {}, f.id)
    assert.equal(back.code, '', f.id)
  }
  const pot = exportFile('po', { en, template: true }).text
  assert.match(pot, /"Language: \\n"/)
  assert.match(pot, /"Plural-Forms: nplurals=INTEGER; plural=EXPRESSION;\\n"/)
  assert.match(pot, /"Content-Type: text\/plain; charset=UTF-8\\n"/)
  assert.match(pot, /#\. Keep \{count\} unchanged\.\nmsgctxt "a\.n"\nmsgid "\{count\} file"\nmsgid_plural "\{count\} files"\nmsgstr\[0\] ""\nmsgstr\[1\] ""/)
  assert.match(pot, /msgid ""\n"Line one\\n"\n"Line two"/)
  const csv = exportFile('csv', { en, template: true }).text
  assert.ok(csv.startsWith('\uFEFFkey,english,translation,notes\r\n'))
  assert.ok(csv.includes('a.n#one,{count} file,,a; plural form: one; keep {count} unchanged\r\n'))
  assert.match(exportFile('xliff12', { en, template: true }).text, /<trans-unit id="a.hello">\s*<source>Hello<\/source>\s*<target><\/target>/)
  assert.match(exportFile('yaml', { en, template: true }).text, /^# evrd/)
})

test('PO export: language header and Plural-Forms', () => {
  const it = exportFile('po', { code: 'it', name: 'Italiano', messages: { 'a.n': { one: '{count} file', many: '{count} di file', other: '{count} file' } }, en }).text
  assert.match(it, /"Language: it\\n"/)
  assert.match(it, /"Plural-Forms: nplurals=2; plural=\(n != 1\);\\n"/)
  assert.match(it, /"X-Language-Name: Italiano\\n"/)
  assert.match(it, /msgstr\[0\] "\{count\} file"\nmsgstr\[1\] "\{count\} file"\n/)
  assert.ok(!it.includes('msgstr[2]'))
  assert.equal(poPlurals('tr').header, 'nplurals=2; plural=(n != 1);')
  assert.equal(poPlurals('es').header, 'nplurals=2; plural=(n != 1);')
  assert.equal(poPlurals('pt-BR').header, 'nplurals=2; plural=(n > 1);')
  assert.equal(poPlurals('pt').header, 'nplurals=2; plural=(n != 1);')
  assert.equal(poPlurals('ja').header, 'nplurals=1; plural=0;')
  assert.equal(poPlurals('yue').header, 'nplurals=1; plural=0;') // from Intl
  assert.match(poPlurals('ru').header, /^nplurals=3;/)
})

test('Poedit files', async () => {
  const real = await realEn()
  const po = parseFile('it.po', fixture('poedit-it.po'), real)
  assert.equal(po.format, 'po')
  assert.equal(po.code, 'it')
  assert.equal(po.name, 'Italiano')
  assert.equal(po.fuzzy, 1) // account.saveProfile: skipped
  assert.deepEqual(po.messages, {
    'account.language': 'Lingua dell’app',
    'account.languageBrowser': 'Lingua del browser ({language})', // multi-line
    'format.minutesAgo': { one: '{count} minuto fa', other: '{count} minuti fa' },
    'test.escapes': 'Riga uno\nTab\tqui "citato" barra\\inversa',
    // account.heading (empty), shell.fixThings (half done) and the
    // obsolete #~ entry are untranslated
  })

  // CRLF, a BOM, pt_BR, (n > 1), an entry without msgctxt, a fuzzy header
  const pt = parseFile('evrd.po', fixture('poedit-pt_BR-crlf.po'), { 'account.language': 'Language', ...en, 'format.minutesAgo': real['format.minutesAgo'] })
  assert.equal(pt.code, 'pt-BR')
  assert.equal(pt.name, 'Português (Brasil)')
  assert.equal(pt.fuzzy, 0)
  assert.deepEqual(pt.messages, { 'account.language': 'Idioma', 'format.minutesAgo': { one: 'há {count} minuto', other: 'há {count} minutos' } })

  // nplurals that doesn't fit the language: say so, don't guess
  assert.throws(() => parseFile('ru.po', fixture('poedit-ru-nplurals.po'), real), (e) => e.kind === 'nplurals' && e.vars.nplurals === 2 && e.vars.count === 3 && e.vars.language === 'ru')
})

test('PO details', () => {
  const entries = poEntries('msgid ""\nmsgstr "Language: de\\n"\n\n#~ msgid "old"\n#~ msgstr "alt"\nmsgctxt "a.hello"\nmsgid "Hello"\nmsgstr "Hallo"\nmsgctxt "a.bye"\nmsgid "Bye {name}"\nmsgstr "Tschüss {name}"\n')
  assert.deepEqual(entries.map((e) => e.ctx), [null, 'a.hello', 'a.bye'])
  fails(() => parseFile('x.po', 'msgid "unterminated\nmsgstr ""', en), 'po')
  fails(() => parseFile('x.po', 'garbage here\n', en), 'po')
  fails(() => parseFile('x.po', '# only a comment\n', en), 'po')
  // without a header, English's plural rules
  assert.deepEqual(parseFile('x.po', 'msgctxt "a.n"\nmsgid "f"\nmsgid_plural "fs"\nmsgstr[0] "Datei"\nmsgstr[1] "Dateien"\n', en).messages, { 'a.n': { one: 'Datei', other: 'Dateien' } })
})

test('CSV: RFC 4180, BOM, notes, missing translations', () => {
  assert.deepEqual(csvRows('a,"b ""c""",d\r\n"multi\nline",,x\n'), [['a', 'b "c"', 'd'], ['multi\nline', '', 'x']])
  const p = parseFile('evrd-de.csv', '\uFEFFKey,English,Translation,Notes\na.hello,Hello,Hallo,a note\na.bye,"Bye {name}",,\na.n#one,x,eine,\na.n#other,x,viele\n"a.lines",x,"zwei\nZeilen"\n', en)
  assert.equal(p.code, 'de') // from the file name
  assert.equal(p.name, 'Deutsch')
  assert.deepEqual(p.messages, { 'a.hello': 'Hallo', 'a.n': { one: 'eine', other: 'viele' }, 'a.lines': 'zwei\nZeilen' })
  // a plural without other is half done: untranslated
  assert.deepEqual(parseFile('x.csv', 'key,translation\na.n#one,eine\n', en).messages, {})
  fails(() => parseFile('x.csv', 'id,english\na,b\n', en), 'csvHeader')
  fails(() => parseFile('x.csv', 'key,translation\na,"open\n', en), 'csvQuote')
})

test('JSON: our format, flat, nested, i18next plurals', () => {
  const ours = parseFile('x.json', JSON.stringify({ code: 'de_DE', name: ' Deutsch ', messages: { 'a.hello': 'Hallo', 'a.bye': 'Bye {name}' } }), en)
  assert.deepEqual([ours.code, ours.name, ours.messages], ['de', 'Deutsch', { 'a.hello': 'Hallo' }]) // English text left out
  assert.deepEqual(parseFile('fr.json', '{"a.hello": "Bonjour", "a.bye": ""}', en), { format: 'json', fuzzy: 0, code: 'fr', name: 'Français', messages: { 'a.hello': 'Bonjour' } })
  assert.deepEqual(parseFile('x.json', '{"a": {"hello": "Hallo", "n_one": "eine", "n_other": "viele", "n_few": ""}}', en).messages, { 'a.hello': 'Hallo', 'a.n': { one: 'eine', other: 'viele' } })
  fails(() => parseFile('x.json', '{nope', en), 'json')
  fails(() => parseFile('x.json', '[1, 2]', en), 'notMap')
  fails(() => parseFile('x.json', '{"a": [1]}', en), 'notText')
})

test('XLIFF 1.2 and 2.0 from other tools', () => {
  const v12 = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE xliff>
<xliff version="1.2" xmlns="urn:oasis:names:tc:xliff:document:1.2">
  <file original="x" source-language="en" target-language="es-ES" datatype="plaintext">
    <header><note>made elsewhere</note></header>
    <body>
      <group id="g">
        <trans-unit id="a.hello" approved="yes"><source>Hello</source><target state="translated">Hola</target></trans-unit>
      </group>
      <trans-unit id="a.bye"><source>Bye {name}</source><target><![CDATA[Adiós <b>{name}</b>]]></target></trans-unit>
      <trans-unit id="a.tricky"><source>x</source><target>a &amp; b &lt;c&gt; &#233;&#x00e9; &quot;q&quot;</target></trans-unit>
      <trans-unit id="a.only"><source>x</source><target/></trans-unit>
    </body>
  </file>
</xliff>`
  const a = parseFile('x.xliff', v12, en)
  assert.equal(a.code, 'es')
  assert.deepEqual(a.messages, { 'a.hello': 'Hola', 'a.bye': 'Adiós <b>{name}</b>', 'a.tricky': 'a & b <c> éé "q"' })
  const v2 = `<xliff xmlns="urn:oasis:names:tc:xliff:document:2.0" version="2.0" srcLang="en" trgLang="tr">
<file id="f"><unit id="a.hello"><segment><source>Hello</source><target>Merhaba</target></segment></unit>
<unit id="a.n#one"><segment><source>x</source><target>{count} dosya</target></segment></unit>
<unit id="a.n#other"><segment><source>x</source><target>{count} dosya</target></segment></unit></file></xliff>`
  const b = parseFile('x.xlf', v2, en)
  assert.equal(b.code, 'tr')
  assert.deepEqual(b.messages, { 'a.hello': 'Merhaba', 'a.n': { one: '{count} dosya', other: '{count} dosya' } })
  fails(() => parseFile('x.xlf', '<foo/>', en), 'xliff')
  fails(() => parseFile('x.xlf', '<xliff><file></xliff>', en), 'xml')
  fails(() => xml('<a>'), 'xml')
  // no entity expansion beyond XML's own
  assert.deepEqual(xml('<!DOCTYPE x [<!ENTITY e "boom">]><a>&e;</a>').children[0].children, ['&e;'])
})

test('YAML subset', () => {
  const doc = yaml(`---
# a comment
code: de
name: 'Deutsch ''DE'''
messages:
  a.hello: Hallo   # trailing comment
  "a.bye": "Tschüss {name}\\u0021"
  a:
    lines: "Zeile eins\\nZeile zwei"
    n:
      one: eine
      other: viele
`)
  assert.deepEqual(doc, { code: 'de', name: "Deutsch 'DE'", messages: { 'a.hello': 'Hallo', 'a.bye': 'Tschüss {name}!', a: { lines: 'Zeile eins\nZeile zwei', n: { one: 'eine', other: 'viele' } } } })
  const p = parseFile('x.yml', 'a:\n  hello: Hallo\n', en)
  assert.deepEqual(p.messages, { 'a.hello': 'Hallo' })
  fails(() => yaml('a:\n  - list'), 'yamlUnsupported')
  fails(() => yaml('a: |\n  block'), 'yamlUnsupported')
  fails(() => yaml('a: &anchor x'), 'yamlUnsupported')
  fails(() => yaml('a:\n\tb: tab'), 'yaml')
  fails(() => yaml('a:\n    b: x\n  c: y'), 'yaml')
  fails(() => yaml('just text'), 'yaml')
  fails(() => yaml('a: "open'), 'yaml')
})

test('detecting the format and the language', () => {
  assert.equal(detect('x.POT', ''), 'po')
  assert.equal(detect('x.txt', '  {"a": 1}'), 'json')
  assert.equal(detect('x', '<?xml version="1.0"?><xliff/>'), 'xliff')
  assert.equal(detect('x', '# c\nmsgid ""'), 'po')
  assert.equal(detect('x', 'key,translation'), 'csv')
  assert.equal(detect('x', 'a: b'), 'yaml')
  assert.equal(codeFromName('evrd-pt_BR.po'), 'pt-BR')
  assert.equal(codeFromName('de.json'), 'de')
  assert.equal(codeFromName('evrd-template.pot'), '')
  assert.equal(codeFromName('translation.csv'), '')
  assert.equal(normalizeCode('tr_TR'), 'tr')
  assert.equal(normalizeCode(''), '')
  fails(() => exportFile('docx', { en }), 'unknownFormat')
})
