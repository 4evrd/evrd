import { writable } from 'svelte/store'
import { api } from '../api/client.js'

// networksBloc — owns the user's IRC networks (the "servers" you add), backed
// by the real evrd API.
export function createNetworksBloc() {
  const networks = writable([])
  const busy = writable(false)

  async function load() {
    busy.set(true)
    try {
      networks.set((await api.listNetworks()) ?? [])
    } finally {
      busy.set(false)
    }
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
    if (net.status === 'disconnected') await api.connect(net.id)
    else await api.disconnect(net.id)
    await load()
  }

  return { networks, busy, load, add, remove, toggle }
}
