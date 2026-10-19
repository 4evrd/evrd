// Unit tests for the formatting helpers: pnpm test:unit
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { nodeBadge, device, limit, ratio, chartMax, cap, date, ago, dur, since, clock, kfmt, level, short, netColor, netGlow, hue, greeting } from './format.js'

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


// a fixed "now" for the time-dependent helpers: 2026-06-15 12:00:00 UTC
const NOW = Date.UTC(2026, 5, 15, 12, 0, 0)
const at = (secondsAgo) => NOW / 1000 - secondsAgo

test('times ago, at every scale', (t) => {
  t.mock.timers.enable({ apis: ['Date'], now: NOW })
  assert.equal(ago(0), 'never')
  assert.equal(ago(at(5)), 'just now')
  assert.equal(ago(at(-30)), 'just now') // a clock slightly ahead
  assert.equal(ago(at(60)), '1 minute ago')
  assert.equal(ago(at(150)), '2 minutes ago')
  assert.equal(ago(at(3600)), '1 hour ago')
  assert.equal(ago(at(5 * 3600)), '5 hours ago')
  assert.equal(ago(at(86400)), 'yesterday')
  assert.equal(ago(at(3 * 86400)), '3 days ago')
  assert.equal(ago(at(20 * 86400)), '2 weeks ago')
  assert.match(ago(at(90 * 86400)), /^[A-Z][a-z]{2} \d{1,2}$/) // a date, this year
})

test('dates: the year only when it isn’t this one', (t) => {
  t.mock.timers.enable({ apis: ['Date'], now: NOW })
  assert.equal(date(0), '—')
  assert.equal(date(Date.UTC(2026, 2, 14, 12) / 1000), 'Mar 14')
  assert.equal(date(Date.UTC(2024, 2, 14, 12) / 1000), 'Mar 14, 2024')
})

test('durations and uptimes', (t) => {
  for (const [secs, want] of [[null, '—'], [-1, '—'], [0, 'now'], [59, 'now'], [60, '1m'], [3599, '59m'], [3600, '1h'], [3660, '1h 1m'], [86400, '1d'], [90000, '1d 1h'], [11 * 86400 + 4 * 3600, '11d 4h']]) {
    assert.equal(dur(secs), want, `dur(${secs})`)
  }
  t.mock.timers.enable({ apis: ['Date'], now: NOW })
  assert.equal(since(0), '—')
  assert.equal(since(at(7200)), '2h')
})

test('clock is HH:MM in local time', () => {
  assert.match(clock(NOW / 1000), /^\d\d:\d\d$/)
})

test('counts shorten past ten thousand', () => {
  for (const [n, want] of [[null, '—'], [0, '0'], [9999, '9,999'], [18400, '18.4k'], [123456, '123k'], [2_500_000, '2.5M'], [25_000_000, '25M']]) {
    assert.equal(kfmt(n), want, `kfmt(${n})`)
  }
})

test('usage levels', () => {
  assert.equal(level(1, 0), '') // unlimited
  assert.equal(level(5, 10), '')
  assert.equal(level(8, 10), 'warn')
  assert.equal(level(10, 10), 'bad')
  assert.equal(limit(5), 5)
})

test('network badges and colours', () => {
  assert.equal(short('Libera Chat'), 'LC')
  assert.equal(short('irc.libera.chat'), 'IL')
  assert.equal(short('rizon'), 'RI')
  assert.equal(short('!!!'), '??')
  assert.equal(netColor(0), netColor(6)) // the palette repeats
  assert.notEqual(netColor(0), netColor(1))
  assert.equal(netGlow(1), netGlow(7))
  assert.equal(hue('alice'), hue('alice')) // a nick keeps its colour
  assert.equal(hue(''), hue(undefined))
  assert.equal(cap('hello'), 'Hello')
  assert.equal(cap(''), '')
})

test('greetings follow the time of day', (t) => {
  const local = (h) => new Date(2026, 5, 15, h, 0, 0).getTime()
  for (const [h, want] of [[2, 'Up late'], [9, 'Morning'], [14, 'Afternoon'], [21, 'Evening']]) {
    t.mock.timers.enable({ apis: ['Date'], now: local(h) })
    assert.equal(greeting(), want, `${h}:00`)
    t.mock.timers.reset()
  }
})

test('devices on every platform', () => {
  assert.equal(device('Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0 Safari/537.36 Edg/141.0'), 'Edge on Windows')
  assert.equal(device('Mozilla/5.0 (X11; Linux x86_64; rv:131.0) Gecko/20100101 Firefox/131.0'), 'Firefox on Linux')
  assert.equal(device('Mozilla/5.0 (Linux; Android 15) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0 Mobile Safari/537.36'), 'Chrome on Android')
  assert.equal(device('curl/8.10.1'), 'Browser')
})
