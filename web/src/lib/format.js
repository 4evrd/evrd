import en from './i18n/en/format.js'

// format.js stays plain JS so Node tests can use it: the root layout hands it
// the app's t() and i18n state (useI18n); until then it speaks English.
let i18n = { locale: 'en', intl: 'en' }
let t = (key, vars = {}) => {
  let m = en[key]
  if (typeof m === 'object') m = m[new Intl.PluralRules('en').select(Number(vars.count ?? 0))] ?? m.other
  return m.replace(/\{(\w+)\}/g, (all, name) => (name in vars ? String(vars[name]) : all))
}
export function useI18n(translate, state) {
  t = translate
  i18n = state
}

export const cap = (s) => (s ? s.charAt(0).toUpperCase() + s.slice(1) : '')

// unix seconds -> "Mar 14, 2025" / "Mar 2" this year
export function date(unix) {
  if (!unix) return '—'
  const d = new Date(unix * 1000)
  const sameYear = d.getFullYear() === new Date().getFullYear()
  return d.toLocaleDateString(i18n.intl, sameYear ? { day: 'numeric', month: 'short' } : { day: 'numeric', month: 'short', year: 'numeric' })
}

// unix seconds -> "just now" / "12 minutes ago" / "3 weeks ago"
export function ago(unix) {
  if (!unix) return t('format.never')
  const s = Math.max(0, Math.round(Date.now() / 1000 - unix))
  if (s < 60) return t('format.justNow')
  if (s < 3600) return t('format.minutesAgo', { count: Math.floor(s / 60) })
  if (s < 86400) return t('format.hoursAgo', { count: Math.floor(s / 3600) })
  if (s < 14 * 86400) { const d = Math.floor(s / 86400); return d === 1 ? t('format.yesterday') : t('format.daysAgo', { count: d }) }
  if (s < 60 * 86400) return t('format.weeksAgo', { count: Math.floor(s / 604800) })
  return date(unix)
}

// seconds -> "12m", "6d", "11d 4h", "now"
export function dur(secs) {
  if (secs == null || secs < 0) return '—'
  if (secs < 60) return t('format.now')
  if (secs < 3600) return t('format.minutes', { m: Math.floor(secs / 60) })
  if (secs < 86400) { const h = Math.floor(secs / 3600), m = Math.floor((secs % 3600) / 60); return m ? t('format.hoursMinutes', { h, m }) : t('format.hours', { h }) }
  const d = Math.floor(secs / 86400), h = Math.floor((secs % 86400) / 3600)
  return h ? t('format.daysHours', { d, h }) : t('format.days', { d })
}
export const since = (unix) => (unix ? dur(Math.round(Date.now() / 1000 - unix)) : '—')
// a wait in seconds -> "45s", "30m", "1h 2m" (dur, but seconds under a minute)
export const wait = (secs) => (secs > 0 && secs < 60 ? t('format.seconds', { s: Math.round(secs) }) : dur(secs))

// "HH:MM"
export const clock = (unix) => new Date(unix * 1000).toTimeString().slice(0, 5)

// 18400 -> "18.4k"
export function kfmt(n) {
  if (n == null) return '—'
  if (n >= 1e6) return (n / 1e6).toFixed(n >= 1e7 ? 0 : 1) + 'M'
  if (n >= 1e4) return (n / 1e3).toFixed(n >= 1e5 ? 0 : 1) + 'k'
  return n.toLocaleString(i18n.intl)
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
  return t(h < 5 ? 'format.greetingLate' : h < 12 ? 'format.greetingMorning' : h < 18 ? 'format.greetingAfternoon' : 'format.greetingEvening')
}

// device names a browser from its user agent: "Chrome on macOS".
export function device(ua) {
  if (!ua) return t('format.unknownDevice')
  const b = /Edg\//.test(ua) ? 'Edge' : /Firefox\//.test(ua) ? 'Firefox' : /Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : t('format.browser')
  const os = /iPhone|iPad/.test(ua) ? 'iPhone' : /Android/.test(ua) ? 'Android' : /Mac OS X/.test(ua) ? 'macOS' : /Windows/.test(ua) ? 'Windows' : /Linux/.test(ua) ? 'Linux' : ''
  return os ? t('format.browserOn', { browser: b, os }) : b
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

// --- hourly charts (traffic, reconnects) ---------------------------------------

// hourAt is the point under a pointer at frac (0–1) across a chart of n points.
export const hourAt = (frac, n) => Math.max(0, Math.min(n - 1, Math.round(frac * (n - 1))))

// hourSpan names the hour point i covers: it ends at start + i hours, and the
// last one is the hour in progress.
export const hourSpan = (start, i, n = 25) => (i === n - 1 ? t('format.untilNow', { from: clock(start + (i - 1) * 3600) }) : `${clock(start + (i - 1) * 3600)}–${clock(start + i * 3600)}`)

// hourRows is each series' value at point i, the biggest first.
export const hourRows = (series, i) => series.map((s) => ({ name: s.name, color: s.color, value: s.points[i] || 0 })).sort((a, b) => b.value - a.value)

// lanes stacks labels that would overlap: each mark ({x, w} in px) takes
// the first of `max` lanes whose last label ended before it (plus a gap);
// a mark that fits none gets lane -1 (shown on hover only).
export function lanes(marks, max = 3, gap = 6) {
  const ends = []
  return [...marks].sort((a, b) => a.x - b.x).map((m) => {
    let lane = ends.findIndex((e) => e + gap <= m.x)
    if (lane < 0 && ends.length < max) lane = ends.length
    if (lane >= 0) ends[lane] = m.x + m.w
    return { ...m, lane }
  })
}
