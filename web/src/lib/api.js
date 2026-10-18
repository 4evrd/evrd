// Tiny fetch wrapper for the evrd API. The session is an HttpOnly cookie, so
// there's no token to handle here. A 401 means "not signed in".
export class Unauthorized extends Error {}

// ApiError keeps the HTTP status and the JSON body so pages can tell a
// validation problem (422) from "try again later".
export class ApiError extends Error {
  constructor(message, status, data) {
    super(message)
    this.status = status
    this.data = data || {}
  }
}

async function req(method, path, body) {
  const res = await fetch(path, {
    method,
    headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  let data = null
  try { data = await res.json() } catch { /* no body */ }
  if (res.status === 401) throw new Unauthorized((data && data.error) || 'sign in first')
  if (!res.ok) throw new ApiError((data && data.error) || `HTTP ${res.status}`, res.status, data)
  return data
}

export const api = {
  config: () => req('GET', '/api/config'),
  passwordLogin: (username, password) => req('POST', '/auth/password', { username, password }),
  logout: () => req('POST', '/auth/logout'),

  me: () => req('GET', '/api/me'),
  updateMe: (patch) => req('PATCH', '/api/me', patch),
  deleteMe: (confirm) => req('DELETE', '/api/me', { confirm }),
  changePassword: (current, next) => req('POST', '/api/me/password', { current, next }),
  sessions: () => req('GET', '/api/me/sessions'),
  revokeSession: (id) => req('DELETE', `/api/me/sessions/${encodeURIComponent(id)}`),
  revokeOtherSessions: () => req('POST', '/api/me/sessions/revoke-others'),
  clientPasswords: () => req('GET', '/api/me/client-passwords'),
  createClientPassword: (name) => req('POST', '/api/me/client-passwords', { name }),
  deleteClientPassword: (id) => req('DELETE', `/api/me/client-passwords/${id}`),

  networks: () => req('GET', '/api/networks'),
  addNetwork: (net) => req('POST', '/api/networks', net),
  updateNetwork: (id, patch) => req('PATCH', `/api/networks/${id}`, patch),
  removeNetwork: (id) => req('DELETE', `/api/networks/${id}`),
  connect: (id) => req('POST', `/api/networks/${id}/connect`),
  disconnect: (id) => req('POST', `/api/networks/${id}/disconnect`),
  messages: (id, target) => req('GET', `/api/networks/${id}/messages?target=${encodeURIComponent(target)}`),
  buffers: (id) => req('GET', `/api/networks/${id}/buffers`),

  // openStream connects a network's live message WebSocket (same origin, so
  // the session cookie authenticates it). onMessage gets each message.
  openStream(id, onMessage, onClose) {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
    const ws = new WebSocket(`${proto}//${location.host}/api/networks/${id}/stream`)
    ws.onmessage = (e) => { try { onMessage(JSON.parse(e.data)) } catch { /* ignore malformed frames */ } }
    ws.onclose = () => onClose?.()
    const out = (frame) => ws.readyState === WebSocket.OPEN && (ws.send(JSON.stringify(frame)), true)
    return {
      // kind: undefined (a message) | 'action' | 'notice'
      send: (target, text, kind) => out({ target, text, kind }),
      // a raw IRC line: JOIN, NICK, TOPIC, …
      raw: (line) => out({ raw: line }),
      // a *status command; replies arrive as messages from *status
      status: (command) => out({ status: command }),
      close: () => { ws.onclose = null; ws.close() },
    }
  },

  admin: {
    overview: () => req('GET', '/api/admin/overview'),
    users: () => req('GET', '/api/admin/users'),
    createUser: (u) => req('POST', '/api/admin/users', u),
    user: (id) => req('GET', `/api/admin/users/${id}`),
    updateUser: (id, patch) => req('PATCH', `/api/admin/users/${id}`, patch),
    deleteUser: (id) => req('DELETE', `/api/admin/users/${id}`),
    setPassword: (id, password) => req('POST', `/api/admin/users/${id}/password`, { password }),
    revokeSessions: (id) => req('POST', `/api/admin/users/${id}/sessions/revoke`),
  },
}
