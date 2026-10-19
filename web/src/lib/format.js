export const cap = (s) => (s ? s.charAt(0).toUpperCase() + s.slice(1) : '')

// unix seconds -> "Mar 14, 2025" / "Mar 2" this year
export function date(unix) {
  if (!unix) return '—'
  const d = new Date(unix * 1000)
  const sameYear = d.getFullYear() === new Date().getFullYear()
  return d.toLocaleDateString('en', sameYear ? { day: 'numeric', month: 'short' } : { day: 'numeric', month: 'short', year: 'numeric' })
}

// unix seconds -> "just now" / "12 minutes ago" / "3 weeks ago"
export function ago(unix) {
  if (!unix) return 'never'
  const s = Math.max(0, Math.round(Date.now() / 1000 - unix))
  if (s < 60) return 'just now'
  if (s < 3600) { const m = Math.floor(s / 60); return `${m} minute${m === 1 ? '' : 's'} ago` }
  if (s < 86400) { const h = Math.floor(s / 3600); return `${h} hour${h === 1 ? '' : 's'} ago` }
  if (s < 14 * 86400) { const d = Math.floor(s / 86400); return d === 1 ? 'yesterday' : `${d} days ago` }
  if (s < 60 * 86400) return `${Math.floor(s / 604800)} weeks ago`
  return date(unix)
}

// seconds -> "12m", "6d", "11d 4h", "now"
export function dur(secs) {
  if (secs == null || secs < 0) return '—'
  if (secs < 60) return 'now'
  if (secs < 3600) return `${Math.floor(secs / 60)}m`
  if (secs < 86400) { const h = Math.floor(secs / 3600), m = Math.floor((secs % 3600) / 60); return m ? `${h}h ${m}m` : `${h}h` }
  const d = Math.floor(secs / 86400), h = Math.floor((secs % 86400) / 3600)
  return h ? `${d}d ${h}h` : `${d}d`
}
export const since = (unix) => (unix ? dur(Math.round(Date.now() / 1000 - unix)) : '—')

// "HH:MM"
export const clock = (unix) => new Date(unix * 1000).toTimeString().slice(0, 5)

// 18400 -> "18.4k"
export function kfmt(n) {
  if (n == null) return '—'
  if (n >= 1e6) return (n / 1e6).toFixed(n >= 1e7 ? 0 : 1) + 'M'
  if (n >= 1e4) return (n / 1e3).toFixed(n >= 1e5 ? 0 : 1) + 'k'
  return n.toLocaleString('en')
}

// A limit for display: 0 is unlimited.
export const limit = (n) => (n === 0 ? '∞' : n)
export const ratio = (used, max) => (max ? Math.min(1, used / max) : 0)
export const level = (used, max) => (max && used / max >= 1 ? 'bad' : max && used / max >= 0.75 ? 'warn' : '')

// A network's two-letter badge and colour.
export function short(name) {
  const words = name.replace(/[^\p{L}\p{N} ._-]/gu, '').split(/[\s._-]+/).filter(Boolean)
  if (words.length >= 2) return (words[0][0] + words[1][0]).toUpperCase()
  return (name.replace(/[^\p{L}\p{N}]/gu, '').slice(0, 2) || '??').toUpperCase()
}
const NET_COLORS = ['var(--primary)', 'var(--nick)', 'var(--signal)', 'var(--ok)', 'var(--warn)', 'var(--ice)']
const NET_GLOWS = ['oklch(80% 0.10 205 / 0.16)', 'oklch(80% 0.07 240 / 0.14)', 'oklch(70% 0.16 45 / 0.16)', 'oklch(80% 0.11 170 / 0.14)', 'oklch(82% 0.13 80 / 0.12)', 'oklch(90% 0.03 215 / 0.10)']
export const netColor = (i) => NET_COLORS[i % NET_COLORS.length]
export const netGlow = (i) => NET_GLOWS[i % NET_GLOWS.length]

// Nick colours in chat.
const HUES = ['var(--nick)', 'var(--cyan)', 'var(--warn)', 'var(--signal)', 'var(--ice)', 'var(--ok)']
export const hue = (n) => HUES[[...(n || '')].reduce((a, c) => a + c.charCodeAt(0), 0) % HUES.length]

// A greeting for the time of day.
export function greeting() {
  const h = new Date().getHours()
  return h < 5 ? 'Up late' : h < 12 ? 'Morning' : h < 18 ? 'Afternoon' : 'Evening'
}

// device names a browser from its user agent: "Chrome on macOS".
export function device(ua) {
  if (!ua) return 'Unknown device'
  const b = /Edg\//.test(ua) ? 'Edge' : /Firefox\//.test(ua) ? 'Firefox' : /Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : 'Browser'
  const os = /iPhone|iPad/.test(ua) ? 'iPhone' : /Android/.test(ua) ? 'Android' : /Mac OS X/.test(ua) ? 'macOS' : /Windows/.test(ua) ? 'Windows' : /Linux/.test(ua) ? 'Linux' : ''
  return os ? `${b} on ${os}` : b
}

// nodeBadge is a node's one-letter badge: node-a → A, a host name → its
// first letter.
export const nodeBadge = (id) => (/[-_]([a-z0-9])$/i.exec(id || '')?.[1] || id?.[0] || '?').toUpperCase()

// chartMax is a round top for a chart's scale. One spike shouldn't flatten
// everything else: past 4× the 98th percentile the scale stops, and the
// spike runs off the top.
export function chartMax(values) {
  const vs = values.filter((v) => v != null).sort((a, b) => a - b)
  let m = vs.at(-1) || 0
  const p98 = vs[Math.floor(vs.length * 0.98)] || 0
  if (vs.length >= 20 && p98 > 0 && m > 4 * p98) m = 4 * p98
  if (m <= 0) return 1
  const p = Math.pow(10, Math.floor(Math.log10(m)))
  for (const k of [1, 2, 2.5, 5, 10]) if (k * p >= m * 1.05) return k * p
  return 10 * p
}
