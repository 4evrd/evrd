export const cap = (s) => (s ? s.charAt(0).toUpperCase() + s.slice(1) : '')

// unix seconds -> "14 Mar 2026"
export function date(unix) {
  if (!unix) return '—'
  return new Date(unix * 1000).toLocaleDateString('en', { day: 'numeric', month: 'short', year: 'numeric' })
}

// unix seconds -> "3 min ago" / "2 days ago" / a date for anything older
export function ago(unix) {
  if (!unix) return 'never'
  const s = Math.max(0, Math.round(Date.now() / 1000 - unix))
  if (s < 60) return 'just now'
  if (s < 3600) return `${Math.floor(s / 60)} min ago`
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`
  if (s < 30 * 86400) { const d = Math.floor(s / 86400); return `${d} day${d === 1 ? '' : 's'} ago` }
  return date(unix)
}

// "HH:MM" for a chat line
export const clock = (unix) => new Date(unix * 1000).toTimeString().slice(0, 5)

// A limit for display: 0 is unlimited.
export const limit = (n) => (n === 0 ? '∞' : n)

// Usage as a percent of a limit (0 when unlimited).
export const usePct = (used, max) => (max ? Math.min(100, (100 * used) / max) : 0)

export const level = (pct) => (pct >= 100 ? 'bad' : pct >= 80 ? 'warn' : '')

// A short, readable name for a browser user agent.
export function device(ua) {
  if (!ua) return 'Unknown device'
  const browser = /Edg\//.test(ua) ? 'Edge' : /Firefox\//.test(ua) ? 'Firefox' : /Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : /curl|Go-http/i.test(ua) ? 'Script' : 'Browser'
  const os = /iPhone|iPad/.test(ua) ? 'iOS' : /Android/.test(ua) ? 'Android' : /Mac OS X/.test(ua) ? 'macOS' : /Windows/.test(ua) ? 'Windows' : /Linux/.test(ua) ? 'Linux' : ''
  return os ? `${browser} on ${os}` : browser
}
