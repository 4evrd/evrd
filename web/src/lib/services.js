// Logging in to a network's services (docs/services.md): the methods, and
// presets that fill in what well-known networks need.

export const METHODS = [
  ['auto', 'Automatic — SASL, else NickServ'],
  ['sasl', 'SASL only'],
  ['nickserv', 'IDENTIFY to NickServ'],
  ['command', 'A command of my own'],
  ['none', 'Don’t log in'],
]

const blank = { servicesNick: '', identifyArgs: '', identifyOn: '', ghost: '', authCommand: '' }

// Each preset sets these fields; quakenet also switches the q module on.
export const PRESETS = [
  { key: 'auto', label: 'Automatic (SASL, then NickServ)', set: { authMethod: 'auto', ...blank } },
  { key: 'sasl', label: 'SASL only (Libera.Chat, ergo…)', set: { authMethod: 'sasl', ...blank } },
  { key: 'atheme', label: 'NickServ — Atheme or Anope', set: { authMethod: 'nickserv', ...blank, identifyArgs: 'account', ghost: 'regain' } },
  { key: 'dalnet', label: 'DALnet', set: { authMethod: 'nickserv', ...blank, servicesNick: 'NickServ@services.dal.net', identifyArgs: 'password', ghost: 'ghost' } },
  { key: 'undernet', label: 'Undernet (X)', set: { authMethod: 'command', ...blank, authCommand: 'PRIVMSG X@channels.undernet.org :LOGIN %account% %password%' } },
  { key: 'quakenet', label: 'QuakeNet (Q, with the q module)', set: { authMethod: 'none', ...blank }, module: 'q' },
  { key: 'custom', label: 'Custom', set: {} },
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
export function checkAuth(f) {
  const e = {}
  if (/[\s,:!]/.test(f.servicesNick || '')) e.servicesNick = 'One word, like NickServ or NickServ@services.dal.net.'
  if (f.authMethod === 'command' && !(f.authCommand || '').trim()) e.authCommand = 'Enter the command that logs you in.'
  const t = String(f.authTimeout ?? '').trim()
  if (t !== '' && !(Number.isInteger(+t) && +t >= 0 && +t <= 300)) e.authTimeout = 'Seconds, 0–300 (0 = 15).'
  return e
}

// authLabel says how this connection's login went.
export function authLabel(auth) {
  return { pending: 'Waiting for services…', identified: 'Logged in', failed: 'Login refused', unconfirmed: 'Services didn’t answer' }[auth] || ''
}
