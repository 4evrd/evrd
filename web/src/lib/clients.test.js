// Unit tests for client labels: pnpm test:unit
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { deviceLabel } from './clients.js'

test("the address isn't said twice", () => {
  assert.equal(deviceLabel({ device: 'SSO password from 203.0.113.5', ip: '203.0.113.5' }), 'SSO password')
  assert.equal(deviceLabel({ device: 'SSO password from 203.0.113.5', ip: '198.51.100.7' }), 'SSO password from 203.0.113.5')
  assert.equal(deviceLabel({ device: 'laptop', ip: '203.0.113.5' }), 'laptop')
  assert.equal(deviceLabel({ device: '', ip: '' }), '')
  assert.equal(deviceLabel({}), '')
})
