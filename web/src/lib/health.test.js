import { test } from 'node:test'
import assert from 'node:assert/strict'
import { RANGES, rangeOf, slotState, slotTimes, ticks, stackTotals, peakIndex, uptimeTone, lagTone, counted } from './health.js'

test('rangeOf', () => {
  for (const r of RANGES) assert.equal(rangeOf(r), r)
  assert.equal(rangeOf(null), '24h')
  assert.equal(rangeOf('1y'), '24h')
})

test('slotState', () => {
  assert.equal(slotState(null), 'none')
  assert.equal(slotState(undefined), 'none')
  assert.equal(slotState(1), 'up')
  assert.equal(slotState(0.999), 'up')
  assert.equal(slotState(0.5), 'part')
  assert.equal(slotState(0), 'down')
})

test('slotTimes', () => {
  assert.deepEqual(slotTimes(1000, 3600, 0, 24), { from: 1000, to: 4600, current: false })
  assert.deepEqual(slotTimes(1000, 3600, 23, 24), { from: 1000 + 23 * 3600, to: 1000 + 24 * 3600, current: true })
})

test('ticks', () => {
  assert.deepEqual(ticks(24, 3600), [0, 4, 8, 12, 16, 20, 24])
  assert.deepEqual(ticks(168, 3600), [0, 24, 48, 72, 96, 120, 144, 168])
  assert.deepEqual(ticks(30, 86400), [0, 5, 10, 15, 20, 25, 30])
  assert.deepEqual(ticks(7, 86400), [0, 5, 7])
})

test('stackTotals and peakIndex', () => {
  assert.deepEqual(stackTotals([{ points: [1, 0, 2] }, { points: [0, 0, 3] }]), [1, 0, 5])
  assert.deepEqual(stackTotals([]), [])
  assert.equal(peakIndex([0, 3, 1, 3]), 3)
  assert.equal(peakIndex([0, 0]), -1)
})

test('tones', () => {
  assert.equal(uptimeTone(null), '')
  assert.equal(uptimeTone(100), 'ok')
  assert.equal(uptimeTone(95), 'warn')
  assert.equal(uptimeTone(50), 'bad')
  assert.equal(lagTone(undefined), '')
  assert.equal(lagTone(40), 'ok')
  assert.equal(lagTone(800), 'warn')
  assert.equal(lagTone(5000), 'bad')
})

test('counted', () => {
  assert.deepEqual(counted([1, 1, 0.5, 0, null]), { up: 2, part: 1, down: 1, none: 1 })
  assert.deepEqual(counted(undefined), { up: 0, part: 0, down: 0, none: 0 })
})
