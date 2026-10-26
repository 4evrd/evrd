// Unit tests for the network picker's order: pnpm test:unit
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { sortNets } from './netsort.js'

const nets = [
  { name: 'libera', status: 'disconnected' },
  { name: 'EFnet', status: 'connected' },
  { name: 'oftc', status: 'error' },
  { name: 'dalnet', status: 'reconnecting' },
  { name: 'net10', status: 'connected' },
  { name: 'net9', status: 'connected' },
]
const names = (l) => l.map((n) => n.name)

test('by name ignores case and counts numbers', () => {
  assert.deepEqual(names(sortNets(nets, 'name')), ['dalnet', 'EFnet', 'libera', 'net9', 'net10', 'oftc'])
})

test('by status puts live links first, then by name', () => {
  assert.deepEqual(names(sortNets(nets, 'status')), ['EFnet', 'net9', 'net10', 'dalnet', 'oftc', 'libera'])
})

test("sorting doesn't change the list it's given", () => {
  const before = names(nets)
  sortNets(nets, 'status')
  assert.deepEqual(names(nets), before)
})
