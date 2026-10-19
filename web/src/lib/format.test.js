// Unit tests for the formatting helpers: node --test src/lib/
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { nodeBadge, device, limit, ratio, chartMax } from './format.js'

test('node badges: the node letter, or a host name’s first letter', () => {
  assert.equal(nodeBadge('node-a'), 'A')
  assert.equal(nodeBadge('evrd_b'), 'B')
  // a single node named after its host used to get its last letter ("G")
  assert.equal(nodeBadge('ircforever.org'), 'I')
  assert.equal(nodeBadge(''), '?')
})

test('devices are named from the user agent', () => {
  assert.equal(device('Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0 Safari/537.36'), 'Chrome on macOS')
  assert.equal(device('Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1'), 'Safari on iPhone')
  assert.equal(device(''), 'Unknown device')
})

test('limits: 0 is unlimited', () => {
  assert.equal(limit(0), '∞')
  assert.equal(ratio(3, 0), 0)
  assert.equal(ratio(3, 2), 1)
})

test('chart scales: round tops, and a lone spike doesn’t flatten the rest', () => {
  assert.equal(chartMax([3, 7, null, 42]), 50)
  assert.equal(chartMax([]), 1)
  // 25ms lag all day and one 100s timeout: the scale stays near the lag
  const lag = Array(95).fill(25)
  lag.push(100000)
  assert.equal(chartMax(lag), 200)
})
