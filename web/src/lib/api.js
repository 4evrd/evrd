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

// language is the one the app shows (setLanguage): sent with each request,
// so evrd's own messages (errors, the connection log, events) come back in it.
let language = ''
export function setLanguage(code) { language = code || '' }

async function req(method, path, body) {
  const headers = {}
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  if (language) headers['X-Evrd-Language'] = language
  const res = await fetch(path, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  let data = null
  try { data = await res.json() } catch { /* no body */ }
  if (res.status === 401) throw new Unauthorized((data && data.error) || 'sign in first')
  if (!res.ok) throw new ApiError((data && data.error) || `HTTP ${res.status}`, res.status, data)
  return data
}

const q = (o) => new URLSearchParams(o).toString()

export const api = {
  config: () => req('GET', '/api/config'),
  // language packs (public: the sign-in page uses them)
  languages: () => req('GET', '/api/languages'),
  language: (code) => req('GET', `/api/languages/${encodeURIComponent(code)}`),
  // evrd's IRC texts (*status), the irc.* keys of a pack: a built-in
  // language's, and how many each language has
  ircMessages: (code) => req('GET', `/api/irc-messages/${encodeURIComponent(code)}`),
  ircCoverage: () => req('GET', '/api/irc-messages'),
  passwordLogin: (username, password) => req('POST', '/auth/password', { username, password }),
  logout: () => req('POST', '/auth/logout'),

  me: () => req('GET', '/api/me'),
  updateMe: (patch) => req('PATCH', '/api/me', patch),
  deleteMe: (confirm) => req('DELETE', '/api/me', { confirm }),
  changePassword: (current, next) => req('POST', '/api/me/password', { current, next }),
  sessions: () => req('GET', '/api/me/sessions'),
  revokeSession: (id) => req('DELETE', `/api/me/sessions/${encodeURIComponent(id)}`),
  revokeOtherSessions: () => req('POST', '/api/me/sessions/revoke-others'),
  // second factors for IRC sign-ins
  mfa: () => req('GET', '/api/me/mfa'),
  updateMFA: (patch) => req('PATCH', '/api/me/mfa', patch),
  startTOTP: () => req('POST', '/api/me/mfa/totp'),
  confirmTOTP: (code) => req('POST', '/api/me/mfa/totp/confirm', { code }),
  removeTOTP: (code) => req('POST', '/api/me/mfa/totp/remove', { code }),
  newRecoveryCodes: (code) => req('POST', '/api/me/mfa/recovery', { code }),
  // IRC sign-in by link: the page a client's link opens
  ircSignIn: (token) => req('GET', `/api/irc-signins/${encodeURIComponent(token)}`),
  allowIRCSignIn: (token, remember) => req('POST', `/api/irc-signins/${encodeURIComponent(token)}/allow`, { remember }),
  denyIRCSignIn: (token) => req('POST', `/api/irc-signins/${encodeURIComponent(token)}/deny`),
  clientPasswords: () => req('GET', '/api/me/client-passwords'),
  createClientPassword: (name) => req('POST', '/api/me/client-passwords', { name }),
  deleteClientPassword: (id) => req('DELETE', `/api/me/client-passwords/${id}`),
  myLanguagePack: () => req('GET', '/api/me/language-pack'),
  putMyLanguagePack: (pack) => req('PUT', '/api/me/language-pack', pack),
  deleteMyLanguagePack: () => req('DELETE', '/api/me/language-pack'),
  clients: () => req('GET', '/api/me/clients'),
  clientDrops: () => req('GET', '/api/me/client-drops'),
  events: (limit = 50) => req('GET', `/api/me/events?limit=${limit}`),
  overview: () => req('GET', '/api/overview'),
  reconnects: () => req('GET', '/api/reconnects'),

  networks: () => req('GET', '/api/networks'),
  network: (id) => req('GET', `/api/networks/${id}`),
  bindHosts: () => req('GET', '/api/bindhosts'),
  addNetwork: (net) => req('POST', '/api/networks', net),
  updateNetwork: (id, patch) => req('PATCH', `/api/networks/${id}`, patch),
  removeNetwork: (id) => req('DELETE', `/api/networks/${id}`),
  connect: (id) => req('POST', `/api/networks/${id}/connect`),
  disconnect: (id) => req('POST', `/api/networks/${id}/disconnect`),
  messages: (id, target) => req('GET', `/api/networks/${id}/messages?${q({ target })}`),
  buffers: (id) => req('GET', `/api/networks/${id}/buffers`),
  networkLog: (id) => req('GET', `/api/networks/${id}/log`),
  networkHealth: (id, range) => req('GET', `/api/networks/${id}/health?${q({ range })}`),
  modules: () => req('GET', '/api/modules'),
  directory: () => req('GET', '/api/directory'),
  channel: (id, target) => req('GET', `/api/networks/${id}/channel?${q({ target })}`),
  markRead: (id, target = '') => req('POST', `/api/networks/${id}/read`, { target }),
  playback: (id) => req('GET', `/api/networks/${id}/playback`),
  setPlayback: (id, change) => req('PATCH', `/api/networks/${id}/playback`, change),
  clearPlayback: (id, buffers) => req('POST', `/api/networks/${id}/playback/clear`, { buffers }),

  // openStream connects a network's live message WebSocket (same origin, so
  // the session cookie authenticates it). onMessage gets each message.
  // onReady runs once the server confirms the subscription ({type:
  // "ready"}); only then is nothing missed. onTyping gets others' typing
  // ({target, nick, state: active|paused|done}).
  openStream(id, onMessage, onClose, onReady, onTyping) {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
    const ws = new WebSocket(`${proto}//${location.host}/api/networks/${id}/stream${language ? '?lang=' + encodeURIComponent(language) : ''}`)
    ws.onmessage = (e) => {
      let m
      try { m = JSON.parse(e.data) } catch { return /* ignore malformed frames */ }
      if (m.type === 'ready') { onReady?.(); return }
      if (m.type === 'typing') { onTyping?.(m); return }
      if (m.type) return // other control frames
      onMessage(m)
    }
    ws.onclose = () => onClose?.()
    const out = (frame) => ws.readyState === WebSocket.OPEN && (ws.send(JSON.stringify(frame)), true)
    return {
      // kind: undefined (a message) | 'action' | 'notice'
      send: (target, text, kind) => out({ target, text, kind }),
      // a raw IRC line: JOIN, NICK, TOPIC, …
      raw: (line) => out({ raw: line }),
      // a *status command; replies arrive as messages from *status
      status: (command) => out({ status: command }),
      // the user's typing in target: active, paused or done
      typing: (target, state) => out({ target, typing: state }),
      close: () => { ws.onclose = null; ws.close() },
    }
  },

  admin: {
    overview: () => req('GET', '/api/admin/overview'),
    settings: () => req('GET', '/api/admin/settings'),
    saveSettings: (set, reset) => req('PATCH', '/api/admin/settings', { set, reset }),
    refreshDirectory: () => req('POST', '/api/admin/directory/refresh'),
    putLanguage: (pack) => req('PUT', `/api/admin/languages/${encodeURIComponent(pack.code)}`, pack),
    deleteLanguage: (code) => req('DELETE', `/api/admin/languages/${encodeURIComponent(code)}`),
    users: () => req('GET', '/api/admin/users'),
    createUser: (u) => req('POST', '/api/admin/users', u),
    user: (id) => req('GET', `/api/admin/users/${id}`),
    userOverview: (id) => req('GET', `/api/admin/users/${id}/overview`),
    userReconnects: (id) => req('GET', `/api/admin/users/${id}/reconnects`),
    networkLog: (id, nid) => req('GET', `/api/admin/users/${id}/networks/${nid}/log`),
    networkHealth: (id, nid, range) => req('GET', `/api/admin/users/${id}/networks/${nid}/health?${q({ range })}`),
    metrics: (range) => req('GET', `/api/admin/metrics?range=${encodeURIComponent(range)}`),
    clientDrops: (range, sort) => req('GET', `/api/admin/client-drops?${q({ range, sort })}`),
    updateUser: (id, patch) => req('PATCH', `/api/admin/users/${id}`, patch),
    deleteUser: (id) => req('DELETE', `/api/admin/users/${id}`),
    // no password: one is generated and returned; either way it's temporary
    resetPassword: (id, password) => req('POST', `/api/admin/users/${id}/password`, password ? { password } : undefined),
    revokeSessions: (id) => req('POST', `/api/admin/users/${id}/sessions/revoke`),
  },
}
