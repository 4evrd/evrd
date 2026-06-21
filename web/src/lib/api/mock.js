// Mock evrd admin API.
//
// Stands in for the daemon's admin endpoints until they exist. Backed by
// localStorage so sessions and networks survive a refresh, which makes the UI
// feel real to develop against. Swap this single module for a real fetch()/WS
// client later — the BLoCs that import it don't change.

const SESSION_KEY = 'evrd.session'
const NETWORKS_KEY = 'evrd.networks'

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

function read(key, fallback) {
  try {
    const v = JSON.parse(localStorage.getItem(key))
    return v ?? fallback
  } catch {
    return fallback
  }
}
function write(key, val) {
  localStorage.setItem(key, JSON.stringify(val))
}

export const api = {
  async login(username, password) {
    await sleep(300)
    if (!username || !password) throw new Error('Username and password required')
    const session = { user: username, token: `mock-${username}` }
    write(SESSION_KEY, session)
    return session
  },
  session() {
    return read(SESSION_KEY, null)
  },
  logout() {
    localStorage.removeItem(SESSION_KEY)
  },

  async listNetworks() {
    await sleep(150)
    return read(NETWORKS_KEY, [])
  },
  async addNetwork(net) {
    await sleep(200)
    const list = read(NETWORKS_KEY, [])
    const id = (list.at(-1)?.id ?? 0) + 1
    const created = { id, status: 'disconnected', ...net }
    write(NETWORKS_KEY, [...list, created])
    return created
  },
  async removeNetwork(id) {
    await sleep(150)
    write(
      NETWORKS_KEY,
      read(NETWORKS_KEY, []).filter((n) => n.id !== id),
    )
  },
  async setStatus(id, status) {
    await sleep(250)
    const list = read(NETWORKS_KEY, []).map((n) => (n.id === id ? { ...n, status } : n))
    write(NETWORKS_KEY, list)
    return list.find((n) => n.id === id)
  },
}
