import { writable } from 'svelte/store'
import { api } from '../api/mock.js'

// networksBloc — owns the user's IRC networks (the "servers" you add).
export function createNetworksBloc() {
  const networks = writable([])
  const busy = writable(false)

  async function load() {
    busy.set(true)
    networks.set(await api.listNetworks())
    busy.set(false)
  }

  async function add(net) {
    const created = await api.addNetwork(net)
    networks.update((l) => [...l, created])
    return created
  }

  async function remove(id) {
    await api.removeNetwork(id)
    networks.update((l) => l.filter((n) => n.id !== id))
  }

  async function toggle(net) {
    const next = net.status === 'connected' ? 'disconnected' : 'connected'
    const updated = await api.setStatus(net.id, next)
    networks.update((l) => l.map((n) => (n.id === net.id ? updated : n)))
  }

  return { networks, busy, load, add, remove, toggle }
}
