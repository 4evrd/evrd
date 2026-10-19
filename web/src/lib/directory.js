// The network directory (GET /api/directory): public IRC networks a user
// picks from when adding one, instead of typing server details.

const hay = (n) => [n.id, n.name, n.description, n.region, ...(n.tags || [])].filter(Boolean).join(' ').toLowerCase()

// searchNetworks keeps the networks whose id, name, description, tags or
// region contain every word of query (all of them for an empty query).
export function searchNetworks(nets, query) {
  const words = String(query || '').toLowerCase().split(/\s+/).filter(Boolean)
  return (nets || []).filter((n) => { const h = hay(n); return words.every((w) => h.includes(w)) })
}

// connectServers are the servers to save: the TLS ones when there are any
// (a plain-text fallback would quietly give up TLS), else all of them. Like
// directory.Network.Connect in the daemon.
export function connectServers(n) {
  const all = n.servers || []
  const tls = all.filter((s) => s.tls)
  return tls.length ? tls : all
}

// serverLabel shows a server the way *status does: host:+port for TLS.
export const serverLabel = (s) => (s ? `${s.host}:${s.tls ? '+' : ''}${s.port}` : '')

// fromDirectory is what picking a network fills in on the add-network form:
// its name (the short id), servers and suggested channels. Identity is left
// to the user.
export function fromDirectory(n) {
  return {
    name: n.id,
    servers: connectServers(n).map((s) => ({ host: s.host, port: String(s.port), tls: !!s.tls, hasPassword: false, password: undefined })),
    channels: (n.channels || []).map((c) => ({ name: c, key: '', detached: false, disabled: false })),
  }
}

// loginHint says how to sign in to the network's services, when the
// directory knows.
export function loginHint(n) {
  if (n.sasl) return 'Supports SASL: put your account below to log in while connecting.'
  if (n.services && n.services !== 'none') return `Services: ${n.services}.`
  return ''
}
