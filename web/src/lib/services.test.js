// Unit tests for the services login presets: pnpm test:unit
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { METHODS, PRESETS, presetOf, applyPreset, usesPassword, checkAuth, authLabel } from './services.js'

const form = (extra = {}) => ({ authMethod: 'auto', servicesNick: '', identifyArgs: '', identifyOn: '', ghost: '', authCommand: '', modules: {}, ...extra })

test('every preset is recognised after it is applied', () => {
  for (const p of PRESETS) {
    const f = applyPreset(form({ authMethod: 'sasl', servicesNick: 'X' }), p.key)
    assert.equal(presetOf(f), p.key === 'custom' ? 'custom' : p.key, p.key)
  }
  assert.ok(METHODS.some(([k]) => k === 'command'))
})

test('stored networks from before presets read as automatic', () => {
  assert.equal(presetOf({ authMethod: 'auto' }), 'auto')
  assert.equal(presetOf(form({ servicesNick: 'NS' })), 'custom')
})

test('presets fill in what networks need', () => {
  const dal = applyPreset(form(), 'dalnet')
  assert.equal(dal.servicesNick, 'NickServ@services.dal.net')
  assert.equal(dal.identifyArgs, 'password')
  const x = applyPreset(form(), 'undernet')
  assert.equal(x.authMethod, 'command')
  assert.match(x.authCommand, /^PRIVMSG X@channels\.undernet\.org :LOGIN %account% %password%$/)
  const custom = applyPreset(form({ ghost: 'ghost' }), 'custom')
  assert.equal(custom.ghost, 'ghost')
  assert.equal(applyPreset(form(), 'nope').authMethod, 'auto')
})

test('QuakeNet switches the q module on, and leaving it off again', () => {
  const f = applyPreset({ authMethod: 'auto' }, 'quakenet')
  assert.equal(f.modules.q.enabled, true)
  assert.equal(f.authMethod, 'none')
  assert.equal(usesPassword(f), true)
  applyPreset(f, 'atheme')
  assert.equal(f.modules.q.enabled, false)
  assert.equal(usesPassword(form({ authMethod: 'none' })), false)
})

test('login fields are checked', () => {
  assert.deepEqual(checkAuth(form()), {})
  assert.deepEqual(Object.keys(checkAuth(form({ servicesNick: 'Nick Serv', authMethod: 'command', authTimeout: '301' }))).sort(), ['authCommand', 'authTimeout', 'servicesNick'])
  assert.deepEqual(checkAuth(form({ authTimeout: '' })), {})
  assert.deepEqual(checkAuth({ authMethod: 'auto' }), {})
})

test('the login state reads as words', () => {
  assert.equal(authLabel('identified'), 'Logged in')
  assert.equal(authLabel('failed'), 'Login refused')
  assert.equal(authLabel(''), '')
})
