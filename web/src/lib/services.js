// Logging in to a network's services (docs/services.md): the methods, and
// presets that fill in what well-known networks need. Text is message keys
// (i18n/<lang>/services.js): labels are keys for t(), and the functions that
// return text take t, English by default so plain Node needs no Svelte.

import en from './i18n/en/services.js'

const english = (key, vars = {}) => en[key].replace(/\{(\w+)\}/g, (all, n) => (n in vars ? String(vars[n]) : all))

export const METHODS = [
  ['auto', 'services.method.auto'],
  ['sasl', 'services.method.sasl'],
  ['nickserv', 'services.method.nickserv'],
  ['command', 'services.method.command'],
  ['none', 'services.method.none'],
]

const blank = { servicesNick: '', identifyArgs: '', identifyOn: '', ghost: '', authCommand: '' }

// Each preset sets these fields (label is a message key); quakenet also switches the q module on.
export const PRESETS = [
  { key: 'auto', label: 'services.preset.auto', set: { authMethod: 'auto', ...blank } },
  { key: 'sasl', label: 'services.preset.sasl', set: { authMethod: 'sasl', ...blank } },
  { key: 'atheme', label: 'services.preset.atheme', set: { authMethod: 'nickserv', ...blank, identifyArgs: 'account', ghost: 'regain' } },
  { key: 'dalnet', label: 'services.preset.dalnet', set: { authMethod: 'nickserv', ...blank, servicesNick: 'NickServ@services.dal.net', identifyArgs: 'password', ghost: 'ghost' } },
  { key: 'undernet', label: 'services.preset.undernet', set: { authMethod: 'command', ...blank, authCommand: 'PRIVMSG X@channels.undernet.org :LOGIN %account% %password%' } },
  { key: 'quakenet', label: 'services.preset.quakenet', set: { authMethod: 'none', ...blank }, module: 'q' },
  { key: 'custom', label: 'services.preset.custom', set: {} },
]

// presetOf names the preset a form matches, else custom.
export function presetOf(f) {
  const q = !!f.modules?.q?.enabled
  for (const p of PRESETS) {
    if (p.key === 'custom' || q !== (p.module === 'q')) continue
    if (Object.entries(p.set).every(([k, v]) => (f[k] || '') === v)) return p.key
  }
  return 'custom'
}

// applyPreset fills a form in from a preset (custom leaves it as it is).
// Leaving QuakeNet switches the q module off again.
export function applyPreset(f, key) {
  const p = PRESETS.find((x) => x.key === key)
  if (!p || key === 'custom') return f
  Object.assign(f, p.set)
  f.modules ||= {}
  if (p.module) f.modules[p.module] = { enabled: true, settings: { ...(f.modules[p.module]?.settings || {}) } }
  else if (f.modules.q?.enabled) f.modules.q.enabled = false
  return f
}

// usesPassword: whether the account and password fields matter.
export const usesPassword = (f) => f.authMethod !== 'none' || !!f.modules?.q?.enabled

// checkAuth validates the login fields: { field: message } for what's wrong.
export function checkAuth(f, t = english) {
  const e = {}
  if (/[\s,:!]/.test(f.servicesNick || '')) e.servicesNick = t('services.badServicesNick')
  if (f.authMethod === 'command' && !(f.authCommand || '').trim()) e.authCommand = t('services.badAuthCommand')
  const secs = String(f.authTimeout ?? '').trim()
  if (secs !== '' && !(Number.isInteger(+secs) && +secs >= 0 && +secs <= 300)) e.authTimeout = t('services.badAuthTimeout')
  return e
}

// authLabel says how this connection's login went.
export function authLabel(auth, t = english) {
  return ['pending', 'identified', 'failed', 'unconfirmed'].includes(auth) ? t(`services.auth.${auth}`) : ''
}
