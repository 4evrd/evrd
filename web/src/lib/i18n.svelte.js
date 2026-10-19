// The web app's languages. Text lives in message files, one per area of the
// app and language: i18n/<lang>/<area>.js, each a default export of flat
// "area.key": "text" entries. English is built in; the others load when
// picked. A key a language lacks falls back to English.
//
// t('networks.retrying', { wait: '4m' }) fills {wait}; an entry that is an
// object ({ one, other }, or any Intl plural category) picks by vars.count.
// Sentences with markup inside use <Msg> (components/Msg.svelte).

import { LANGUAGES, pick } from './locale.js'

export { LANGUAGES, pick }

const en = merge(import.meta.glob('./i18n/en/*.js', { eager: true, import: 'default' }))
const lazy = import.meta.glob(['./i18n/*/*.js', '!./i18n/en/*.js'], { import: 'default' })

export const i18n = $state({ locale: 'en', messages: en })

function merge(mods) {
  return Object.assign({}, ...Object.values(mods))
}

// setLocale switches the app to a language, loading its messages first.
export async function setLocale(code) {
  if (!LANGUAGES.some((l) => l.code === code)) code = 'en'
  let messages = en
  if (code !== 'en') {
    const files = Object.entries(lazy).filter(([path]) => path.startsWith(`./i18n/${code}/`))
    messages = { ...en, ...merge(Object.fromEntries(await Promise.all(files.map(async ([p, load]) => [p, await load()])))) }
  }
  i18n.messages = messages
  i18n.locale = code
  if (typeof document !== 'undefined') document.documentElement.lang = code
}

// t is a key's text in the current language, with {name}s filled from vars.
export function t(key, vars = {}) {
  return fill(entry(key, vars), vars)
}

// entry is a key's raw text (plural form picked), or the key itself when
// no language has it, so a missing one shows rather than vanishing.
export function entry(key, vars = {}) {
  let m = i18n.messages[key] ?? en[key]
  if (m && typeof m === 'object') {
    const form = new Intl.PluralRules(i18n.locale).select(Number(vars.count ?? 0))
    m = m[form] ?? m.other
  }
  return m ?? key
}

function fill(text, vars) {
  return text.replace(/\{(\w+)\}/g, (all, name) => (name in vars ? String(vars[name]) : all))
}
