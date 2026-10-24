// Language packs: translations uploaded by admins (for everyone) or by a
// user (for themselves), as {code, name, messages}. The server checks the
// same rules as problems() here; the English keys are only known here.
// No Svelte, so plain Node tests can use it (docs/translations.md).

export const CODE = /^[a-z]{2,3}(-[A-Za-z0-9]{2,8})?$/
export const KEY = /^[A-Za-z0-9_.-]{1,100}$/
// The Intl plural categories, in order.
export const FORMS = ['zero', 'one', 'two', 'few', 'many', 'other']
export const MAX_SIZE = 512 * 1024
export const MAX_TEXT = 2000

// isPlural: an object of plural forms ({ one, other }…), not nested keys.
export const isPlural = (v) =>
  !!v && typeof v === 'object' && !Array.isArray(v) && Object.keys(v).length > 0 && Object.keys(v).every((k) => FORMS.includes(k))

// layered is the messages to use: each key from the last layer that has it.
// The layers go English, the built-in language, the server's pack, the
// user's own pack; missing ones are skipped.
export function layered(...layers) {
  return Object.assign({}, ...layers.filter(Boolean))
}

// coverage is how many of en's keys the layers have between them:
// { have, total }.
export function coverage(en, ...layers) {
  const all = layered(...layers)
  const keys = Object.keys(en || {})
  return { have: keys.filter((k) => k in all).length, total: keys.length }
}

// languageList is the languages to offer: the built-in ones, then the
// server's packs, then the user's own, once per code. A pack for a code
// that's already there renames it when it has a name.
export function languageList(builtin, server = [], personal = null) {
  const out = builtin.map((l) => ({ ...l }))
  for (const p of [...(server || []), personal].filter(Boolean)) {
    const have = out.find((l) => l.code === p.code)
    if (!have) out.push({ code: p.code, name: p.name || p.code })
    else if (p.name) have.name = p.name
  }
  return out
}

// pluralCategories are the plural forms a language uses, in FORMS order
// (Intl's; English's when Intl doesn't know the code).
export function pluralCategories(code) {
  let cats = ['one', 'other']
  try { cats = new Intl.PluralRules(pluralLocale(code)).resolvedOptions().pluralCategories } catch { /* English's */ }
  return FORMS.filter((f) => cats.includes(f))
}

// pluralLocale is code if Intl has plural rules for it, else 'en'.
export function pluralLocale(code) {
  try { return Intl.PluralRules.supportedLocalesOf([code]).length ? code : 'en' } catch { return 'en' }
}

// displayName is a language's name in itself, for prefilling forms ('' if
// Intl doesn't know it).
export function displayName(code) {
  try {
    const n = new Intl.DisplayNames([code], { type: 'language' }).of(code)
    return n && n !== code ? n.charAt(0).toLocaleUpperCase(code) + n.slice(1) : ''
  } catch { return '' }
}

// holes are a message's {placeholders}, sorted, as one string.
export const holes = (v) =>
  [...new Set((v && typeof v === 'object' ? Object.values(v).join(' ') : String(v ?? '')).match(/\{\w+\}/g) || [])].sort().join(' ')

// flatten turns nested objects into "a.b.c" keys, keeping plural forms
// whole. i18next-style plural keys (key_one, key_other) are gathered into
// one plural when English has key as a plural. Values that aren't text are
// reported in bad.
export function flatten(obj, en = {}, bad = []) {
  const out = {}
  const walk = (o, prefix) => {
    for (const [k, v] of Object.entries(o)) {
      const key = prefix ? `${prefix}.${k}` : k
      if (typeof v === 'string' || isPlural(v)) out[key] = v
      else if (v && typeof v === 'object' && !Array.isArray(v)) walk(v, key)
      else bad.push(key)
    }
  }
  walk(obj, '')
  for (const key of Object.keys(out)) {
    const m = key.match(/^(.+)_(zero|one|two|few|many|other)$/)
    if (!m || key in en || !isPlural(en[m[1]]) || typeof out[key] !== 'string') continue
    const p = out[m[1]] && typeof out[m[1]] === 'object' ? out[m[1]] : (out[m[1]] = {})
    p[m[2]] = out[key]
    delete out[key]
  }
  return out
}

// problems finds what the server would refuse in a pack: [{ kind, key? }].
// Kinds: code, name, key, long, form, other, size.
export function problems(pack) {
  const out = []
  if (!CODE.test(pack.code || '')) out.push({ kind: 'code' })
  const n = [...(pack.name || '')].length
  if (n < 1 || n > 40) out.push({ kind: 'name' })
  for (const [k, v] of Object.entries(pack.messages || {})) {
    if (!KEY.test(k)) { out.push({ kind: 'key', key: k }); continue }
    const texts = typeof v === 'string' ? [v] : v && typeof v === 'object' ? Object.values(v) : [null]
    if (typeof v !== 'string') {
      if (!v || typeof v !== 'object' || !Object.keys(v).every((f) => FORMS.includes(f)) || texts.some((x) => typeof x !== 'string')) { out.push({ kind: 'form', key: k }); continue }
      if (!('other' in v)) out.push({ kind: 'other', key: k })
    }
    if (texts.some((x) => [...x].length > MAX_TEXT)) out.push({ kind: 'long', key: k })
  }
  if (new TextEncoder().encode(JSON.stringify(pack)).length > MAX_SIZE) out.push({ kind: 'size' })
  return out
}

// report is what to tell the uploader about a pack against English:
// errors (problems', plus placeholders that differ from English: these block
// saving), unknown keys English doesn't have (a warning), and how many
// English keys are still untranslated.
export function report(pack, en) {
  const errors = problems(pack)
  const unknown = [], placeholders = []
  for (const [k, v] of Object.entries(pack.messages || {})) {
    if (!(k in en)) unknown.push(k)
    else if (holes(v) !== holes(en[k])) placeholders.push(k)
  }
  for (const key of placeholders) errors.push({ kind: 'placeholders', key, want: holes(en[key]) || '—' })
  const untranslated = Object.keys(en).filter((k) => !(k in (pack.messages || {}))).length
  return { errors, unknown, untranslated, total: Object.keys(en).length, count: Object.keys(pack.messages || {}).length }
}
