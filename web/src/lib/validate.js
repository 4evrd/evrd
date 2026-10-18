// Client-side checks that mirror the daemon's rules so mistakes show inline.
// The daemon stays the authority; its error is shown when it disagrees.
export const RULES = {
  required: (v) => (String(v ?? '').trim() ? '' : 'This field is required.'),
  name: (v) => !v.trim() ? 'Give it a name so you can tell it apart.' : v.trim().length > 32 ? 'Keep it to 32 characters or fewer.' : '',
  netname: (v) => !v.trim() ? 'Give it a name so you can tell it apart.' : v.trim().length > 32 ? 'Keep it to 32 characters or fewer.' : /[\s/:]/.test(v.trim()) ? 'No spaces, / or : — the name goes in your IRC server password.' : '',
  host: (v) => !v.trim() ? 'Enter the server address, e.g. irc.libera.chat.' : /^[a-z0-9.-]+$/i.test(v.trim()) ? '' : 'That doesn’t look like a hostname — use letters, digits, dots and dashes, e.g. irc.oftc.net.',
  port: (v) => { const n = +v; return Number.isInteger(n) && n > 0 && n < 65536 ? '' : 'Ports are whole numbers from 1 to 65535 — 6697 for TLS, 6667 without.' },
  nick: (v) => !String(v).trim() ? 'Pick a nickname.' : /^[A-Za-z[\]\\`_^{|}][A-Za-z0-9[\]\\`_^{|}-]{0,29}$/.test(String(v).trim()) ? '' : 'Nicks start with a letter and use letters, digits or [ ] \\ ` _ ^ { | } -.',
  optnick: (v) => (!String(v).trim() ? '' : RULES.nick(v)),
  channels: (v) => !v.trim() || v.split(/[\s,]+/).filter(Boolean).every((c) => /^[#&+!][^\s,]{1,49}$/.test(c)) ? '' : 'Separate channels with commas; each starts with # (e.g. #evrd, #go-nuts).',
  username: (v) => /^[a-z][a-z0-9_-]{1,31}$/.test(v.trim().toLowerCase()) ? '' : 'Use 2–32 lowercase letters, digits, - or _, starting with a letter.',
  count: (v) => (String(v).trim() === '' || /^\d+$/.test(String(v).trim()) ? '' : 'Enter a whole number, 0 or more — or leave it empty for the default.'),
  password: (v) => ([...(v || '')].length >= 10 ? '' : 'Use at least 10 characters.'),
  email: (v) => (!v.trim() || /^[^\s@]+@[^\s@]+$/.test(v.trim()) ? '' : 'That doesn’t look like an email address.'),
}

// check runs space-separated rule names against a value.
export const check = (rules, v, extra) => rules.split(' ').filter(Boolean).map((r) => (r === 'match' ? (v === extra ? '' : 'The passwords don’t match.') : RULES[r]?.(v ?? ''))).find(Boolean) || ''

export const splitChannels = (v) => v.split(/[\s,]+/).map((c) => c.trim()).filter(Boolean)
