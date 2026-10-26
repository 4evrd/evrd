// Unit tests for the dropdown's keyboard logic: pnpm test:unit
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { step, edge, typeahead, indexOf, filter } from './select.js'

const opts = [{ value: 'a', label: 'Alpha' }, { value: 'b', label: 'Beta', disabled: true }, { value: 'c', label: 'Charlie' }, { value: 'd', label: 'alpine' }]

test('arrows skip disabled options and stop at the ends', () => {
  assert.equal(step(opts, 0, 1), 2)
  assert.equal(step(opts, 2, -1), 0)
  assert.equal(step(opts, 3, 1), 3)
  assert.equal(step(opts, 0, -1), 0)
  assert.equal(step(opts, -1, 1), 0)
  assert.equal(step(opts, -1, -1), 3)
  assert.equal(step([{ value: 1, label: 'x', disabled: true }], -1, 1), -1)
})

test('Home and End go to the first and last enabled option', () => {
  assert.equal(edge(opts, false), 0)
  assert.equal(edge(opts, true), 3)
  assert.equal(edge([{ label: 'x', disabled: true }, { label: 'y' }, { label: 'z', disabled: true }], true), 1)
})

test('typing jumps to a matching label; one letter again cycles', () => {
  assert.equal(typeahead(opts, 'a', -1), 0)
  assert.equal(typeahead(opts, 'a', 0), 3)
  assert.equal(typeahead(opts, 'a', 3), 0)
  assert.equal(typeahead(opts, 'alpi', 0), 3)
  assert.equal(typeahead(opts, 'alp', 0), 0)
  assert.equal(typeahead(opts, 'b', 0), -1) // only a disabled one
  assert.equal(typeahead(opts, 'zz', 0), -1)
  assert.equal(typeahead(opts, '', 0), -1)
})

test('the option holding a value', () => {
  assert.equal(indexOf(opts, 'c'), 2)
  assert.equal(indexOf(opts, 'nope'), -1)
})

test('filter matches anywhere in the label or extra text, ignoring case', () => {
  const nets = [{ value: 1, label: 'Libera', find: 'irc.libera.chat' }, { value: 2, label: 'EFnet', find: 'irc.efnet.org' }]
  assert.deepEqual(filter(nets, 'LIB').map((o) => o.value), [1])
  assert.deepEqual(filter(nets, '.org').map((o) => o.value), [2])
  assert.deepEqual(filter(nets, '  ').map((o) => o.value), [1, 2])
  assert.deepEqual(filter(nets, 'nope'), [])
})
