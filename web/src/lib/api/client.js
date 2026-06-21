// Real evrd admin API client (REST + WebSocket). Replaces the mock.
// Base URL is configurable via VITE_API_URL (defaults to the dev daemon).

const BASE = import.meta.env.VITE_API_URL || 'http://localhost:8098'
const SESSION_KEY = 'evrd.session'

function session() {
  try {
    return JSON.parse(localStorage.getItem(SESSION_KEY))
  } catch {
    return null
  }
}
function setSession(s) {
  if (s) localStorage.setItem(SESSION_KEY, JSON.stringify(s))
  else localStorage.removeItem(SESSION_KEY)
}

async function req(method, path, body) {
  const s = session()
  const res = await fetch(BASE + path, {
    method,
    headers: {
      'Content-Type': 'application/json',
      ...(s?.token ? { Authorization: `Bearer ${s.token}` } : {}),
    },
    body: body ? JSON.stringify(body) : undefined,
  })
  if (!res.ok) throw new Error((await res.text()).trim() || res.statusText)
  if (res.status === 204) return null
  return res.json()
}

export const api = {
  session,

  async login(username, password) {
    const s = await req('POST', '/api/login', { username, password })
    setSession(s)
    return s
  },
  logout() {
    setSession(null)
  },

  listNetworks() {
    return req('GET', '/api/networks')
  },
  addNetwork(net) {
    return req('POST', '/api/networks', net)
  },
  removeNetwork(id) {
    return req('DELETE', `/api/networks/${id}`)
  },
  connect(id) {
    return req('POST', `/api/networks/${id}/connect`)
  },
  disconnect(id) {
    return req('POST', `/api/networks/${id}/disconnect`)
  },
  messages(id, target) {
    return req('GET', `/api/networks/${id}/messages?target=${encodeURIComponent(target)}`)
  },

  // openStream connects the live message WebSocket for a network. onMessage is
  // called with each incoming message; the returned handle can send + close.
  openStream(id, onMessage) {
    const s = session()
    const wsBase = BASE.replace(/^http/, 'ws')
    const ws = new WebSocket(`${wsBase}/api/networks/${id}/stream?token=${s.token}`)
    ws.onmessage = (e) => {
      try {
        onMessage(JSON.parse(e.data))
      } catch {
        // ignore malformed frames
      }
    }
    return {
      send: (target, text) =>
        ws.readyState === WebSocket.OPEN && ws.send(JSON.stringify({ target, text })),
      close: () => ws.close(),
    }
  },
}
