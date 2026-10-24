// The web app's languages. Text lives in message files, one per area of the
// app and language: i18n/<lang>/<area>.js, each a default export of flat
// "area.key": "text" entries. English is built in; the others load when
// picked. Language packs add languages or change strings (langpack.js): a
// key comes from the user's own pack, else the server's pack, else the
// built-in language, else English.
//
// t('networks.retrying', { wait: '4m' }) fills {wait}; an entry that is an
// object ({ one, other }, or any Intl plural category) picks by vars.count.
// Sentences with markup inside use <Msg> (components/Msg.svelte). Text is
// never HTML: packs are uploaded by people.

import { LANGUAGES, pick as pickFrom } from './locale.js'
import { layered, languageList, pluralLocale } from './langpack.js'
import { api, setLanguage } from './api.js'

export { LANGUAGES }

export const en = merge(import.meta.glob('./i18n/en/*.js', { eager: true, import: 'default' }))
const lazy = import.meta.glob(['./i18n/*/*.js', '!./i18n/en/*.js'], { import: 'default' })

// packs: the server's language packs ({ code, name, updatedAt });
// personal: the user's own ({ code, name, updatedAt }) or null. intl is
// the locale for Intl (plurals, numbers, dates): the language's, or 'en'
// when Intl doesn't know it.
export const i18n = $state({ locale: 'en', intl: 'en', messages: en, packs: [], personal: null })

function merge(mods) {
  return Object.assign({}, ...Object.values(mods))
}

// languages are the ones on offer: built in, the server's packs, the
// user's own.
export const languages = () => languageList(LANGUAGES, i18n.packs, i18n.personal)

// pick is the language to use (see locale.js), packs included.
export const pick = (chosen, browser) => pickFrom(chosen, browser, languages().map((l) => l.code))

// builtin is a built-in language's own messages ({} for none).
export async function builtin(code) {
  if (code === 'en') return en
  const files = Object.entries(lazy).filter(([path]) => path.startsWith(`./i18n/${code}/`))
  return merge(Object.fromEntries(await Promise.all(files.map(async ([p, load]) => [p, await load()]))))
}

const cache = new Map()
function fetchOnce(key, get) {
  if (!cache.has(key)) cache.set(key, get().catch((e) => { cache.delete(key); throw e }))
  return cache.get(key)
}

// ircBuiltin is a built-in language's IRC texts: what evrd says over IRC
// (*status), the irc.* keys of a pack. evrd serves them; the app never
// shows them, only puts them in templates and downloads and checks packs
// against them. {} for a language without any.
export async function ircBuiltin(code) {
  return (await fetchOnce(`irc:${code}`, () => api.ircMessages(code).catch(() => ({ messages: {} })))).messages || {}
}

// english is every English text a pack can have: the app's and evrd's IRC
// ones.
export async function english() {
  return { ...en, ...(await ircBuiltin('en')) }
}

// builtinAll is a built-in language's texts, the app's and the IRC ones
// (like builtin, {} for none).
export async function builtinAll(code) {
  const [app, irc] = await Promise.all([builtin(code), ircBuiltin(code)])
  return { ...app, ...irc }
}

// serverPack is a server pack's messages, or null.
export async function serverPack(code) {
  const p = i18n.packs.find((x) => x.code === code)
  return p ? (await fetchOnce(`server:${code}:${p.updatedAt}`, () => api.language(code))).messages : null
}

// personalPack is the user's own pack's messages, or null.
export async function personalPack() {
  const p = i18n.personal
  return p ? (await fetchOnce(`me:${p.code}:${p.updatedAt}`, () => api.myLanguagePack())).messages : null
}

// setPacks updates the packs on offer (after an upload, say), dropping any
// copies of them loaded before.
export function setPacks({ server, personal } = {}) {
  if (server !== undefined) i18n.packs = server || []
  if (personal !== undefined) i18n.personal = personal || null
  cache.clear()
}

// setLocale switches the app to a language, loading its messages first.
export async function setLocale(code) {
  if (!languages().some((l) => l.code === code)) code = 'en'
  const own = i18n.personal?.code === code
  const [base, server, mine] = await Promise.all([
    builtin(code),
    serverPack(code).catch(() => null),
    own ? personalPack().catch(() => null) : null,
  ])
  i18n.messages = layered(en, base, server, mine)
  i18n.intl = pluralLocale(code)
  i18n.locale = code
  setLanguage(code)
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
    const form = new Intl.PluralRules(i18n.intl).select(Number(vars.count ?? 0))
    m = m[form] ?? m.other ?? en[key]?.other
  }
  return typeof m === 'string' ? m : key
}

function fill(text, vars) {
  return text.replace(/\{(\w+)\}/g, (all, name) => (name in vars ? String(vars[name]) : all))
}
