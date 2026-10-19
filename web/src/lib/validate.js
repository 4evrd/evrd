import { t } from './i18n.svelte.js'

// Client-side checks that mirror the daemon's rules so mistakes show inline.
// The daemon stays the authority; its error is shown when it disagrees.
export const RULES = {
  required: (v) => (String(v ?? '').trim() ? '' : t('validate.required')),
  name: (v) => !v.trim() ? t('validate.name') : v.trim().length > 32 ? t('validate.nameLong') : '',
  netname: (v) => !v.trim() ? t('validate.name') : v.trim().length > 32 ? t('validate.nameLong') : /[\s/:]/.test(v.trim()) ? t('validate.netnameChars') : '',
  host: (v) => !v.trim() ? t('validate.hostEmpty') : /^[a-z0-9.-]+$/i.test(v.trim()) ? '' : t('validate.host'),
  port: (v) => { const n = +v; return Number.isInteger(n) && n > 0 && n < 65536 ? '' : t('validate.port') },
  nick: (v) => !String(v).trim() ? t('validate.nickEmpty') : /^[A-Za-z[\]\\`_^{|}][A-Za-z0-9[\]\\`_^{|}-]{0,29}$/.test(String(v).trim()) ? '' : t('validate.nick'),
  optnick: (v) => (!String(v).trim() ? '' : RULES.nick(v)),
  channels: (v) => !v.trim() || v.split(/[\s,]+/).filter(Boolean).every((c) => /^[#&+!][^\s,]{1,49}$/.test(c)) ? '' : t('validate.channels'),
  username: (v) => /^[a-z][a-z0-9_-]{1,31}$/.test(v.trim().toLowerCase()) ? '' : t('validate.username'),
  count: (v) => (String(v).trim() === '' || /^\d+$/.test(String(v).trim()) ? '' : t('validate.count')),
  password: (v) => ([...(v || '')].length >= 10 ? '' : t('validate.password')),
  email: (v) => (!v.trim() || /^[^\s@]+@[^\s@]+$/.test(v.trim()) ? '' : t('validate.email')),
}

// check runs space-separated rule names against a value.
export const check = (rules, v, extra) => rules.split(' ').filter(Boolean).map((r) => (r === 'match' ? (v === extra ? '' : t('validate.match')) : RULES[r]?.(v ?? ''))).find(Boolean) || ''

export const splitChannels = (v) => v.split(/[\s,]+/).map((c) => c.trim()).filter(Boolean)
