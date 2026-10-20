// Translation files in common formats, to and from a language pack
// ({code, name, messages}, see langpack.js). Everything is converted here,
// in the browser; the server only ever sees a pack. No Svelte, so plain
// Node tests can use it (docs/translations.md).
//
//   json   {code, name, messages}, a flat {key: text} or nested objects
//   csv    key,english,translation[,notes]
//   po     gettext (Poedit): msgctxt = key, msgid = English, msgstr = text
//   xliff  1.2 (trans-unit) or 2.0 (unit): id = key, source, target
//   yaml   a flat or nested map (a small subset: no lists, anchors or
//          block scalars)
//
// Plural forms are objects in JSON and YAML, msgid_plural in PO, and
// key#one, key#other… rows or units in CSV and XLIFF.

import { FORMS, isPlural, flatten, pluralCategories, displayName } from './langpack.js'

export const FORMATS = [
  { id: 'json', label: 'JSON', ext: 'json', mime: 'application/json' },
  { id: 'csv', label: 'CSV', ext: 'csv', mime: 'text/csv' },
  { id: 'po', label: 'PO (Poedit)', ext: 'po', mime: 'text/x-gettext-translation' },
  // the gettext template: offered only as a template (a language is a .po)
  { id: 'pot', label: 'POT (gettext template)', ext: 'pot', templateOnly: true, mime: 'text/x-gettext-translation' },
  { id: 'xliff12', label: 'XLIFF 1.2', ext: 'xlf', mime: 'application/xliff+xml' },
  { id: 'xliff2', label: 'XLIFF 2.0', ext: 'xlf', mime: 'application/xliff+xml' },
  { id: 'yaml', label: 'YAML', ext: 'yaml', mime: 'application/yaml' },
]

const DOCS = 'https://github.com/4evrd/evrd/blob/main/docs/translations.md'

// FormatError is a file that can't be read: kind names the problem (the
// web app words it), vars fill it in.
export class FormatError extends Error {
  constructor(kind, vars = {}) {
    super(`${kind} ${JSON.stringify(vars)}`)
    this.kind = kind
    this.vars = vars
  }
}

// --- reading -----------------------------------------------------------------

// detect is a file's format, by its extension, else by a look at it.
export function detect(filename, text) {
  const ext = String(filename).toLowerCase().split('.').pop()
  const by = { json: 'json', csv: 'csv', po: 'po', pot: 'po', xlf: 'xliff', xliff: 'xliff', yaml: 'yaml', yml: 'yaml' }[ext]
  if (by) return by
  const s = text.replace(/^﻿/, '').trimStart()
  if (s.startsWith('{')) return 'json'
  if (s.startsWith('<')) return 'xliff'
  if (/^msgid\s/m.test(s)) return 'po'
  if (/^"?key"?\s*,/i.test(s)) return 'csv'
  return 'yaml'
}

// codeFromName guesses a language code from a file name like evrd-de.po.
export function codeFromName(filename) {
  const m = String(filename).match(/(?:^|[-_. ])([a-z]{2,3}(?:[-_][A-Za-z0-9]{2,8})?)\.\w+$/)
  return m && m[1] !== 'evrd' ? normalizeCode(m[1]) : ''
}

// normalizeCode turns pt_BR into pt-BR, and it_IT (a region that only
// repeats the language) into it.
export function normalizeCode(code) {
  const [lang, region] = String(code || '').trim().replace('_', '-').split('-')
  if (!lang) return ''
  return region && region.toLowerCase() !== lang.toLowerCase() ? `${lang.toLowerCase()}-${region}` : lang.toLowerCase()
}

// parseFile reads a translation file: { format, code, name, messages,
// untranslated, fuzzy }. code and name are '' when the file doesn't say.
// en is the English messages (to find keys by English text, and gather
// plurals). Throws FormatError.
export function parseFile(filename, text, en = {}) {
  text = String(text).replace(/^﻿/, '')
  const format = detect(filename, text)
  const out = { json: readJSON, csv: readCSV, po: readPO, xliff: readXLIFF, yaml: readYAML }[format](text, en)
  if (!out.code) out.code = codeFromName(filename)
  if (!out.name && out.code) out.name = displayName(out.code)
  return { format, fuzzy: 0, ...out }
}

// tree reads a parsed JSON or YAML document: {code, name, messages} or a
// map of messages. Empty text, and text the same as English (the template
// not yet translated), is left out.
function tree(doc, en) {
  if (!doc || typeof doc !== 'object' || Array.isArray(doc)) throw new FormatError('notMap')
  const wrapped = doc.messages && typeof doc.messages === 'object' && !Array.isArray(doc.messages)
  const bad = []
  const all = flatten(wrapped ? doc.messages : doc, en, bad)
  if (bad.length) throw new FormatError('notText', { key: bad[0] })
  const messages = {}
  for (let [k, v] of Object.entries(all)) {
    if (typeof v === 'object') v = Object.fromEntries(Object.entries(v).filter(([, x]) => x !== ''))
    if (v !== '' && !(k in en && same(v, en[k]))) messages[k] = v
  }
  incomplete(messages)
  return {
    code: wrapped && typeof doc.code === 'string' ? normalizeCode(doc.code) : '',
    name: wrapped && typeof doc.name === 'string' ? doc.name.trim() : '',
    messages,
  }
}

const same = (a, b) => JSON.stringify(a) === JSON.stringify(b)

function readJSON(text, en) {
  let doc
  try { doc = JSON.parse(text) } catch (e) { throw new FormatError('json', { detail: e.message }) }
  return tree(doc, en)
}

// gather turns key#form entries into plurals; a plural needs "other".
function gather(flat) {
  const out = {}
  for (const [k, v] of Object.entries(flat)) {
    const m = k.match(/^(.+)#(zero|one|two|few|many|other)$/)
    if (!m) { out[k] = v; continue }
    const p = out[m[1]] && typeof out[m[1]] === 'object' ? out[m[1]] : (out[m[1]] = {})
    p[m[2]] = v
  }
  return out
}

// --- CSV (RFC 4180) --------------------------------------------------------------

export function csvRows(text) {
  const rows = []
  let row = [], field = '', i = 0, quoted = false, line = 1
  while (i < text.length) {
    const c = text[i]
    if (quoted) {
      if (c === '"' && text[i + 1] === '"') { field += '"'; i += 2; continue }
      if (c === '"') { quoted = false; i++; continue }
      if (c === '\n') line++
      field += c; i++; continue
    }
    if (c === '"' && field === '') { quoted = true; i++; continue }
    if (c === ',') { row.push(field); field = ''; i++; continue }
    if (c === '\r' || c === '\n') {
      row.push(field); rows.push(row); row = []; field = ''
      i += c === '\r' && text[i + 1] === '\n' ? 2 : 1
      line++
      continue
    }
    field += c; i++
  }
  if (quoted) throw new FormatError('csvQuote', { line })
  if (field !== '' || row.length) { row.push(field); rows.push(row) }
  return rows.filter((r) => r.some((f) => f !== ''))
}

function readCSV(text) {
  const rows = csvRows(text)
  const head = (rows.shift() || []).map((h) => h.trim().toLowerCase())
  const col = (...names) => head.findIndex((h) => names.includes(h))
  const key = col('key', 'id'), tr = col('translation', 'target')
  if (key < 0 || tr < 0) throw new FormatError('csvHeader')
  const flat = {}
  for (const r of rows) {
    const k = (r[key] || '').trim(), v = r[tr] ?? ''
    if (k && v !== '') flat[k] = v
  }
  return { code: '', name: '', messages: incomplete(gather(flat)) }
}

// incomplete drops plurals without "other" (half translated): they count
// as untranslated.
function incomplete(messages) {
  for (const [k, v] of Object.entries(messages)) if (typeof v === 'object' && !('other' in v)) delete messages[k]
  return messages
}

const csvField = (s) => (/[",\r\n]/.test(s) || /^\s|\s$/.test(s) ? `"${s.replace(/"/g, '""')}"` : s)

// --- gettext PO --------------------------------------------------------------------

// PO_PLURALS are the usual Plural-Forms for languages that don't use
// English's (n != 1), and which plural category each msgstr[n] is (a
// category listed second gets the same text). The rest use English's.
const PO_PLURALS = [
  [['ja', 'zh', 'ko', 'vi', 'th', 'id', 'ms', 'lo', 'km', 'my'], 'nplurals=1; plural=0;', [['other']]],
  [['fr', 'pt-BR', 'oc', 'ln', 'mg'], 'nplurals=2; plural=(n > 1);', [['one'], ['other']]],
  [['ru', 'uk', 'be'], 'nplurals=3; plural=(n%10==1 && n%100!=11 ? 0 : n%10>=2 && n%10<=4 && (n%100<10 || n%100>=20) ? 1 : 2);', [['one'], ['few'], ['many', 'other']]],
  [['sr', 'hr', 'bs'], 'nplurals=3; plural=(n%10==1 && n%100!=11 ? 0 : n%10>=2 && n%10<=4 && (n%100<10 || n%100>=20) ? 1 : 2);', [['one'], ['few'], ['other']]],
  [['pl'], 'nplurals=3; plural=(n==1 ? 0 : n%10>=2 && n%10<=4 && (n%100<10 || n%100>=20) ? 1 : 2);', [['one'], ['few'], ['many', 'other']]],
  [['cs', 'sk'], 'nplurals=3; plural=(n==1) ? 0 : (n>=2 && n<=4) ? 1 : 2;', [['one'], ['few'], ['other']]],
  [['ro'], 'nplurals=3; plural=(n==1 ? 0 : (n==0 || (n%100 > 0 && n%100 < 20)) ? 1 : 2);', [['one'], ['few'], ['other']]],
  [['lt'], 'nplurals=3; plural=(n%10==1 && n%100!=11 ? 0 : n%10>=2 && (n%100<10 || n%100>=20) ? 1 : 2);', [['one'], ['few'], ['other']]],
  [['sl'], 'nplurals=4; plural=(n%100==1 ? 0 : n%100==2 ? 1 : n%100==3 || n%100==4 ? 2 : 3);', [['one'], ['two'], ['few'], ['other']]],
  [['ar'], 'nplurals=6; plural=(n==0 ? 0 : n==1 ? 1 : n==2 ? 2 : n%100>=3 && n%100<=10 ? 3 : n%100>=11 ? 4 : 5);', [['zero'], ['one'], ['two'], ['few'], ['many'], ['other']]],
]
const ONE_OTHER = ['nplurals=2; plural=(n != 1);', [['one'], ['other']]]

// poPlurals is a language's Plural-Forms and msgstr[n] categories.
export function poPlurals(code) {
  const base = String(code).split('-')[0]
  const row = PO_PLURALS.find(([codes]) => codes.includes(code)) || PO_PLURALS.find(([codes]) => codes.includes(base))
  if (row) return { header: row[1], forms: row[2] }
  const cats = pluralCategories(code)
  if (cats.length === 1) return { header: 'nplurals=1; plural=0;', forms: [['other']] }
  return { header: ONE_OTHER[0], forms: ONE_OTHER[1] }
}

// poForms is which category each msgstr[n] is for a file's language and
// Plural-Forms header: the usual table's, else Intl's order (zero, one,
// two, few, many, other). nplurals must match one of them.
function poForms(code, pluralForms) {
  const n = Number((pluralForms.match(/nplurals\s*=\s*(\d+)/) || [])[1])
  const known = poPlurals(code).forms
  if (!n) return known
  const intl = pluralCategories(code).map((c) => [c])
  const got = [known, intl].find((f) => f.length === n)
  if (!got) throw new FormatError('nplurals', { nplurals: n, language: code, count: known.length, forms: known.map((f) => f[0]).join(', ') })
  return got
}

const PO_ESC = { n: '\n', t: '\t', r: '\r', '"': '"', '\\': '\\', a: '\x07', b: '\b', f: '\f', v: '\v' }
const unquote = (s, line) => {
  const m = s.match(/^"(.*)"\s*$/)
  if (!m) throw new FormatError('po', { line })
  return m[1].replace(/\\(.)/g, (_, c) => PO_ESC[c] ?? c)
}

// poEntries splits a PO file into entries: { ctx, id, plural, str: [],
// fuzzy, obsolete }.
export function poEntries(text) {
  const entries = []
  let e = null, field = null
  const start = () => { e = { ctx: null, id: null, plural: null, str: [], fuzzy: false, obsolete: false }; entries.push(e); field = null }
  const lines = text.split(/\r?\n/)
  let blank = true
  lines.forEach((raw, i) => {
    const line = raw.trim(), no = i + 1
    if (!line) { blank = true; return }
    if (line.startsWith('#~')) { if (blank || !e) start(); e.obsolete = true; blank = false; return }
    if (line.startsWith('#')) {
      // a comment starts the next entry once the last one has its msgstr
      if (blank || !e || e.str.length) start()
      if (line.startsWith('#,') && /\bfuzzy\b/.test(line)) e.fuzzy = true
      blank = false
      return
    }
    const m = line.match(/^(msgctxt|msgid_plural|msgid|msgstr(?:\[(\d+)\])?)\s+(".*")$/)
    if (m) {
      const kw = m[1]
      if (!e || e.obsolete || ((kw === 'msgctxt' || kw === 'msgid') && (e.str.length || (kw === 'msgid' && e.id !== null)))) start()
      blank = false
      const v = unquote(m[3], no)
      if (kw === 'msgctxt') field = ['ctx']
      else if (kw === 'msgid') field = ['id']
      else if (kw === 'msgid_plural') field = ['plural']
      else field = ['str', m[2] === undefined ? 0 : Number(m[2])]
      set(field, v)
      return
    }
    if (line.startsWith('"') && field) { set(field, get(field) + unquote(line, no)); return }
    throw new FormatError('po', { line: no })
  })
  function set(f, v) { if (f[0] === 'str') e.str[f[1]] = v; else e[f[0]] = v }
  function get(f) { return f[0] === 'str' ? e.str[f[1]] : e[f[0]] }
  return entries.filter((x) => x.id !== null && !x.obsolete)
}

// poHeader is the header entry's "Name: value" lines.
export function poHeader(entries) {
  const h = entries.find((e) => e.id === '' && e.ctx === null)
  const out = {}
  for (const line of (h?.str[0] || '').split('\n')) {
    const i = line.indexOf(':')
    if (i > 0) out[line.slice(0, i).trim().toLowerCase()] = line.slice(i + 1).trim()
  }
  return out
}

function readPO(text, en) {
  const entries = poEntries(text)
  if (!entries.length) throw new FormatError('po', { line: 1 })
  const head = poHeader(entries)
  const code = normalizeCode(head.language)
  const pf = head['plural-forms'] && !/INTEGER/.test(head['plural-forms']) ? head['plural-forms'] : ''
  // English text -> key, for entries without msgctxt
  const byText = {}
  for (const [k, v] of Object.entries(en)) {
    const id = typeof v === 'object' ? v.one ?? v.other : v
    byText[id] = id in byText ? null : k
  }
  let forms = null
  const messages = {}
  let fuzzy = 0, unmatched = 0
  for (const e of entries) {
    if (e.id === '' && e.ctx === null) continue // the header
    const key = e.ctx ?? byText[e.id]
    if (!key) { unmatched++; continue }
    if (e.fuzzy) { fuzzy++; continue }
    if (e.plural === null) {
      if (e.str[0]) messages[key] = e.str[0]
      continue
    }
    forms ??= poForms(code || 'en', pf)
    const p = {}
    forms.forEach((cats, i) => { if (e.str[i]) for (const c of cats) p[c] = e.str[i] })
    if (forms.every((_, i) => e.str[i])) messages[key] = p
  }
  return { code, name: head['x-language-name'] || '', messages, fuzzy, unmatched }
}

const poQuote = (s) => '"' + s.replace(/\\/g, '\\\\').replace(/"/g, '\\"').replace(/\t/g, '\\t').replace(/\r/g, '\\r').replace(/\n/g, '\\n') + '"'
// poString is a keyword and its text, split after each newline the way
// Poedit writes it.
function poString(kw, s) {
  const parts = s.split(/(?<=\n)/)
  if (parts.length < 2) return `${kw} ${poQuote(s)}`
  return [`${kw} ""`, ...parts.filter(Boolean).map(poQuote)].join('\n')
}

// --- XLIFF ---------------------------------------------------------------------------

// xml is a small XML reader: elements { name (no namespace prefix), attrs,
// children } and text. No DTDs or entities beyond XML's own, so nothing is
// fetched or expanded.
const TAG = /<(\/?)([\w:.-]+)((?:\s+[\w:.-]+\s*=\s*(?:"[^"]*"|'[^']*'))*)\s*(\/?)>/y

export function xml(text) {
  const root = { name: '#root', attrs: {}, children: [] }
  const stack = [root]
  let i = 0
  const ent = (s) => s.replace(/&(#x[0-9a-f]+|#\d+|lt|gt|amp|quot|apos);/gi, (_, e) =>
    e[0] === '#' ? String.fromCodePoint(e[1] === 'x' || e[1] === 'X' ? parseInt(e.slice(2), 16) : Number(e.slice(1))) : { lt: '<', gt: '>', amp: '&', quot: '"', apos: "'" }[e.toLowerCase()])
  const fail = () => { throw new FormatError('xml', { line: text.slice(0, i).split('\n').length }) }
  while (i < text.length) {
    const lt = text.indexOf('<', i)
    if (lt < 0) { stack.at(-1).children.push(ent(text.slice(i))); break }
    if (lt > i) stack.at(-1).children.push(ent(text.slice(i, lt)))
    i = lt
    const skip = (open, close) => { if (!text.startsWith(open, i)) return false; const j = text.indexOf(close, i); if (j < 0) fail(); i = j + close.length; return true }
    if (text.startsWith('<![CDATA[', i)) {
      const j = text.indexOf(']]>', i)
      if (j < 0) fail()
      stack.at(-1).children.push(text.slice(i + 9, j))
      i = j + 3
      continue
    }
    if (skip('<!--', '-->') || skip('<?', '?>')) continue
    if (text.startsWith('<!', i)) { // a DOCTYPE, maybe with [declarations]: skipped
      const gt = text.indexOf('>', i), br = text.indexOf('[', i)
      const end = br >= 0 && br < gt ? text.indexOf(']>', br) + 1 : gt
      if (end <= 0) fail()
      i = end + 1
      continue
    }
    TAG.lastIndex = i
    const m = TAG.exec(text)
    if (!m) fail()
    const name = m[2].replace(/^.*:/, '')
    i += m[0].length
    if (m[1]) {
      if (stack.length < 2 || stack.at(-1).name !== name) fail()
      stack.pop()
      continue
    }
    const attrs = {}
    for (const a of m[3].matchAll(/([\w:.-]+)\s*=\s*(?:"([^"]*)"|'([^']*)')/g)) attrs[a[1].replace(/^xml:/, 'xml-')] = ent(a[2] ?? a[3])
    const el = { name, attrs, children: [] }
    stack.at(-1).children.push(el)
    if (!m[4]) stack.push(el)
  }
  if (stack.length !== 1) fail()
  return root
}

const els = (node, name) => {
  const out = []
  const walk = (n) => { for (const c of n.children || []) if (typeof c === 'object') { if (c.name === name) out.push(c); walk(c) } }
  walk(node)
  return out
}
const textOf = (n) => (n ? n.children.map((c) => (typeof c === 'string' ? c : textOf(c))).join('') : '')

function readXLIFF(text) {
  const root = xml(text)
  const x = els(root, 'xliff')[0]
  if (!x) throw new FormatError('xliff')
  const v2 = String(x.attrs.version || '').startsWith('2')
  const flat = {}
  let code = ''
  if (v2) {
    code = x.attrs.trgLang || ''
    for (const u of els(x, 'unit')) {
      const t = els(u, 'target').map(textOf).join('')
      if (u.attrs.id && t !== '') flat[u.attrs.id] = t
    }
  } else {
    code = els(x, 'file').map((f) => f.attrs['target-language']).find(Boolean) || ''
    for (const u of els(x, 'trans-unit')) {
      const t = textOf(els(u, 'target')[0])
      if (u.attrs.id && t !== '') flat[u.attrs.id] = t
    }
  }
  return { code: normalizeCode(code), name: '', messages: incomplete(gather(flat)) }
}

const esc = (s) => String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')

// --- YAML (a small subset) -----------------------------------------------------------

const YAML_ESC = { n: '\n', t: '\t', r: '\r', '"': '"', '\\': '\\', '/': '/', 0: '\0', ' ': ' ' }

// yamlScalar reads a key or value: "double" (JSON-like escapes), 'single'
// ('' for '), or plain. rest is what follows it (a comment, or ": …").
function yamlScalar(s, line) {
  if (s[0] === '"') {
    let out = '', i = 1
    for (; i < s.length && s[i] !== '"'; i++) {
      if (s[i] !== '\\') { out += s[i]; continue }
      const c = s[++i]
      if (c === 'u' || c === 'x' || c === 'U') {
        const n = { x: 2, u: 4, U: 8 }[c]
        out += String.fromCodePoint(parseInt(s.slice(i + 1, i + 1 + n), 16))
        i += n
      } else if (c in YAML_ESC) out += YAML_ESC[c]
      else throw new FormatError('yaml', { line })
    }
    if (s[i] !== '"') throw new FormatError('yaml', { line })
    return [out, s.slice(i + 1)]
  }
  if (s[0] === "'") {
    let out = '', i = 1
    for (; i < s.length; i++) {
      if (s[i] === "'" && s[i + 1] === "'") { out += "'"; i++ } else if (s[i] === "'") break
      else out += s[i]
    }
    if (s[i] !== "'") throw new FormatError('yaml', { line })
    return [out, s.slice(i + 1)]
  }
  if (/^[[\]{}|>&*!%@`,?-](\s|$)|^[[\]{}|>&*!%@`]/.test(s)) throw new FormatError('yamlUnsupported', { line })
  const m = s.match(/^(.*?)(\s+#.*|:\s.*|:$)?$/)
  return [m[1].trim(), m[2] || '']
}

// yaml reads nested maps of text: indentation nests, # starts a comment.
export function yaml(text) {
  const root = {}
  const stack = [{ indent: -1, map: root }]
  text.split(/\r?\n/).forEach((raw, i) => {
    const line = i + 1
    if (/^\s*(#.*)?$/.test(raw) || /^(---|\.\.\.)\s*$/.test(raw)) return
    if (/^ *\t/.test(raw)) throw new FormatError('yaml', { line })
    const indent = raw.match(/^ */)[0].length
    const [key, after] = yamlScalar(raw.slice(indent), line)
    const m = after.match(/^:(?:\s+(.*))?$/)
    if (!m || key === '') throw new FormatError('yaml', { line })
    while (stack.at(-1).indent >= indent) stack.pop()
    const parent = stack.at(-1)
    if (parent.child !== undefined && parent.child !== indent) throw new FormatError('yaml', { line })
    parent.child = indent
    const v = (m[1] || '').trim()
    if (v === '' || v.startsWith('#')) {
      const map = {}
      parent.map[key] = map
      stack.push({ indent, map })
      return
    }
    const [value, rest] = yamlScalar(v, line)
    if (rest.trim() && !rest.trim().startsWith('#')) throw new FormatError('yaml', { line })
    parent.map[key] = value
  })
  return root
}

function readYAML(text, en) {
  return tree(yaml(text), en)
}

const yamlKey = (k) => (/^[A-Za-z0-9_][A-Za-z0-9_.-]*$/.test(k) ? k : JSON.stringify(k))

// --- writing -------------------------------------------------------------------------

// exportFile writes a pack, or (template) every English key with an empty
// translation, as format: { filename, text, mime }. en is the English
// messages; messages the translation's.
export function exportFile(format, { code = '', name = '', messages = {}, en = {}, template = false }) {
  const f = FORMATS.find((x) => x.id === format)
  if (!f) throw new FormatError('unknownFormat')
  if (template) { code = ''; name = ''; messages = {} }
  const keys = [...Object.keys(en), ...Object.keys(messages).filter((k) => !(k in en))]
  const text = { json: writeJSON, csv: writeCSV, po: writePO, pot: writePO, xliff12: writeXLIFF12, xliff2: writeXLIFF2, yaml: writeYAML }[format]({ code, name, messages, en, keys, template, pot: format === 'pot' })
  const ext = f.ext
  return { filename: `evrd-${template ? 'template' : code}.${ext}`, text, mime: f.mime }
}

const area = (k) => k.split('.')[0]
const holesOf = (v) => [...new Set((typeof v === 'object' ? Object.values(v).join(' ') : String(v)).match(/\{\w+\}/g) || [])]
// formsOf are the plural forms to write for a key: English's, the
// language's (as gettext counts them) and any the translation has.
const formsOf = (code, en, tr) => FORMS.filter((f) => (en && typeof en === 'object' && f in en) || (tr && typeof tr === 'object' && f in tr) || (code && poPlurals(code).forms.some((c) => c[0] === f)))

function writeJSON({ code, name, messages, en, template }) {
  return JSON.stringify({ code, name, messages: template ? en : messages }, null, 2) + '\n'
}

function writeYAML({ code, name, messages, en, template }) {
  const lines = [
    '# evrd web app translation. Replace the English text with yours and fill in',
    '# code (like de or pt-BR) and name (the language\'s own name). Keep {placeholders}',
    '# as they are. Text left in English isn\'t uploaded. ' + DOCS,
    `code: ${JSON.stringify(code)}`,
    `name: ${JSON.stringify(name)}`,
    'messages:',
  ]
  for (const [k, v] of Object.entries(template ? en : messages)) {
    if (typeof v === 'string') { lines.push(`  ${yamlKey(k)}: ${JSON.stringify(v)}`); continue }
    lines.push(`  ${yamlKey(k)}:`)
    for (const f of FORMS) if (f in v) lines.push(`    ${f}: ${JSON.stringify(v[f])}`)
  }
  return lines.join('\n') + '\n'
}

// rows are [key, english, translation, note] for every key, plural forms
// as key#form.
function rows({ code, messages, en, keys }) {
  const out = []
  for (const k of keys) {
    const e = en[k], tr = messages[k]
    if (isPlural(e) || isPlural(tr)) {
      for (const f of formsOf(code, e, tr)) out.push([`${k}#${f}`, typeof e === 'object' ? e[f] ?? e.other : e ?? '', typeof tr === 'object' ? tr[f] ?? '' : '', k, f])
    } else out.push([k, e ?? '', tr ?? '', k, ''])
  }
  return out
}

const note = (key, en, form) => [area(key), form && `plural form: ${form}`, holesOf(en ?? '').length ? `keep ${holesOf(en).join(' ')} unchanged` : ''].filter(Boolean).join('; ')

function writeCSV(p) {
  const out = ['key,english,translation,notes']
  for (const [k, e, tr, key, form] of rows(p)) out.push([k, e, tr, note(key, p.en[key], form)].map(csvField).join(','))
  return '﻿' + out.join('\r\n') + '\r\n'
}

function writePO({ code, name, messages, en, keys, template, pot }) {
  const plurals = template ? null : poPlurals(code)
  const head = [
    'Project-Id-Version: evrd',
    'MIME-Version: 1.0',
    'Content-Type: text/plain; charset=UTF-8',
    'Content-Transfer-Encoding: 8bit',
    `Language: ${code}`,
    // a .pot has gettext's placeholder; a .po template has none: Poedit
    // fills it in once the language is set
    ...(template && !pot ? [] : [`Plural-Forms: ${template ? 'nplurals=INTEGER; plural=EXPRESSION;' : plurals.header}`]),
    ...(name ? [`X-Language-Name: ${name}`] : []),
    'X-Generator: evrd',
  ]
  const out = [
    template ? '# evrd web app translation template.' : `# evrd web app translation: ${name || code}.`,
    '# Open it in Poedit (or any PO editor) and translate each entry; keep',
    '# {placeholders} like {count} as they are. Upload the .po file in evrd.',
    `# ${DOCS}`,
    'msgid ""',
    'msgstr ""',
    ...head.map((h) => poQuote(h + '\n')),
    '',
  ]
  for (const k of keys) {
    const e = en[k] ?? (typeof messages[k] === 'object' ? messages[k] : k), tr = messages[k]
    out.push(`#. ${area(k)}`)
    if (holesOf(e).length) out.push(`#. Keep ${holesOf(e).join(' ')} unchanged.`)
    out.push(poString('msgctxt', k))
    if (isPlural(e) || isPlural(tr)) {
      const ep = typeof e === 'object' ? e : { other: e }
      out.push(poString('msgid', ep.one ?? ep.other), poString('msgid_plural', ep.other))
      const forms = plurals ? plurals.forms : [['one'], ['other']]
      forms.forEach((cats, i) => out.push(poString(`msgstr[${i}]`, typeof tr === 'object' ? tr[cats[0]] ?? '' : '')))
    } else {
      out.push(poString('msgid', e), poString('msgstr', typeof tr === 'string' ? tr : ''))
    }
    out.push('')
  }
  return out.join('\n')
}

const XLIFF_NOTE = '<!-- evrd web app translation. Fill in each target; keep {placeholders} as they are.\n     Plural forms are separate units: key#one, key#other (add key#few, key#many… if your\n     language has them). ' + DOCS + ' -->'

function writeXLIFF12(p) {
  const out = ['<?xml version="1.0" encoding="UTF-8"?>', XLIFF_NOTE, '<xliff version="1.2" xmlns="urn:oasis:names:tc:xliff:document:1.2">',
    `  <file original="evrd" source-language="en"${p.code ? ` target-language="${esc(p.code)}"` : ''} datatype="plaintext">`, '    <body>']
  for (const [k, e, tr, key, form] of rows(p)) {
    out.push(`      <trans-unit id="${esc(k)}">`, `        <source>${esc(e)}</source>`, `        <target>${esc(tr)}</target>`, `        <note>${esc(note(key, p.en[key], form))}</note>`, '      </trans-unit>')
  }
  out.push('    </body>', '  </file>', '</xliff>')
  return out.join('\n') + '\n'
}

function writeXLIFF2(p) {
  const out = ['<?xml version="1.0" encoding="UTF-8"?>', XLIFF_NOTE,
    `<xliff version="2.0" xmlns="urn:oasis:names:tc:xliff:document:2.0" srcLang="en"${p.code ? ` trgLang="${esc(p.code)}"` : ''}>`, '  <file id="evrd">']
  for (const [k, e, tr, key, form] of rows(p)) {
    out.push(`    <unit id="${esc(k)}">`, `      <notes><note>${esc(note(key, p.en[key], form))}</note></notes>`, '      <segment>', `        <source>${esc(e)}</source>`, `        <target>${esc(tr)}</target>`, '      </segment>', '    </unit>')
  }
  out.push('  </file>', '</xliff>')
  return out.join('\n') + '\n'
}
