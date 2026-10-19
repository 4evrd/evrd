// Unit tests for the network directory helpers: pnpm test:unit
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { searchNetworks, connectServers, serverLabel, fromDirectory, loginHint } from './directory.js'

const LIBERA = {
  id: 'libera', name: 'Libera.Chat', description: 'Free and open-source software', region: 'global', tags: ['foss'],
  servers: [{ host: 'irc.libera.chat', port: 6697, tls: true }, { host: 'irc.libera.chat', port: 6667, tls: false }],
  sasl: true, services: 'atheme', channels: ['#libera'],
}
const EFNET = { id: 'efnet', name: 'EFnet', servers: [{ host: 'irc.efnet.org', port: 6667, tls: false }], services: 'none' }
const UNDERNET = { id: 'undernet', name: 'Undernet', servers: [{ host: 'irc.undernet.org', port: 6667 }], services: 'x' }

test('search matches every word, anywhere, ignoring case', () => {
  const nets = [LIBERA, EFNET, UNDERNET]
  assert.equal(searchNetworks(nets, '').length, 3)
  assert.equal(searchNetworks(nets, undefined).length, 3)
  assert.deepEqual(searchNetworks(nets, 'LIBERA').map((n) => n.id), ['libera'])
  assert.deepEqual(searchNetworks(nets, 'open  source').map((n) => n.id), ['libera'])
  assert.deepEqual(searchNetworks(nets, 'foss global').map((n) => n.id), ['libera'])
  assert.deepEqual(searchNetworks(nets, 'net').map((n) => n.id), ['efnet', 'undernet'])
  assert.equal(searchNetworks(nets, 'nothing').length, 0)
  assert.equal(searchNetworks(null, 'x').length, 0)
})

test('picking prefers TLS servers, and fills name, servers and channels', () => {
  assert.deepEqual(connectServers(LIBERA), [LIBERA.servers[0]])
  assert.deepEqual(connectServers(EFNET), EFNET.servers)
  assert.deepEqual(connectServers({}), [])
  const f = fromDirectory(LIBERA)
  assert.equal(f.name, 'libera')
  assert.deepEqual(f.servers, [{ host: 'irc.libera.chat', port: '6697', tls: true, hasPassword: false, password: undefined }])
  assert.deepEqual(f.channels, [{ name: '#libera', key: '', detached: false, disabled: false }])
  assert.deepEqual(fromDirectory(UNDERNET).servers[0].tls, false)
  assert.deepEqual(fromDirectory(EFNET).channels, [])
})

test('labels and login hints', () => {
  assert.equal(serverLabel(LIBERA.servers[0]), 'irc.libera.chat:+6697')
  assert.equal(serverLabel(EFNET.servers[0]), 'irc.efnet.org:6667')
  assert.equal(serverLabel(undefined), '')
  assert.match(loginHint(LIBERA), /SASL/)
  assert.equal(loginHint(UNDERNET), 'Services: x.')
  assert.equal(loginHint(EFNET), '')
  assert.equal(loginHint({}), '')
})
