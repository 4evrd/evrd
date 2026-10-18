// Who is signed in, how the server is set up, and the counts the shell shows
// (networks, mentions) — shared by every screen. Loaded once by the layout.
import { api, Unauthorized } from './api.js'

export const session = $state({
  phase: 'loading', // loading | signedOut | error | ready
  error: '',
  config: null, // { passwordLogin, sso, ssoLabel, ircHost, ircPorts, version }
  me: null,
  nets: [], // the user's networks (refreshed by pages that load them)
})

export async function load() {
  session.error = ''
  try {
    session.config = await api.config()
    session.me = await api.me()
    session.phase = 'ready'
    refreshNets()
  } catch (e) {
    if (e instanceof Unauthorized) session.phase = 'signedOut'
    else { session.error = e.message; session.phase = 'error' }
  }
}

export async function refreshMe() {
  session.me = await api.me()
}

export async function refreshNets() {
  try { session.nets = (await api.networks()) || [] } catch { /* keep what we have */ }
}

export async function logout() {
  let next = '/'
  try { next = (await api.logout())?.redirect || '/' } catch { /* signed out locally either way */ }
  location.href = next
}

// The port a native client should use: TLS when there is one.
export function ircPort() {
  const ports = session.config?.ircPorts || []
  return ports.find((p) => p.tls) || ports[0] || { port: 6697, tls: true }
}

export const mentionsTotal = () => session.nets.reduce((a, n) => a + (n.mentions || 0), 0)
