// The order networks are listed in the chat's network picker: by name, or
// by link status (connected first, then on the way up, failed, off), by
// name within each.
const RANK = { connected: 0, connecting: 1, reconnecting: 1, error: 2 }
const rank = (n) => RANK[n.status] ?? 3

const byName = (a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: 'base', numeric: true })

export function sortNets(nets, by) {
  const list = [...nets]
  return by === 'status' ? list.sort((a, b) => rank(a) - rank(b) || byName(a, b)) : list.sort(byName)
}
