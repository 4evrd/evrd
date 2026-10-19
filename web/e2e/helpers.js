import { request as pwRequest, expect } from '@playwright/test'

export const USER = 'e2e'
export const PASS = 'e2epassword1'
export const ADMIN = 'e2eadmin'
export const ADMIN_PASS = 'e2eadminpass'
export const BASE = process.env.E2E_BASE_URL || 'http://localhost:5173'
// The dev stack's upstream IRC server, as evrd (inside compose) reaches it.
export const IRCD = { host: 'ircd', port: 6667, tls: false }

// login drives the sign-in form and waits for the app.
export async function login(page, user = USER, pass = PASS) {
  await page.goto('/')
  await page.getByRole('textbox', { name: 'Username' }).fill(user)
  await page.getByLabel('Password', { exact: true }).fill(pass)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await page.getByRole('heading', { name: 'Overview', exact: true }).waitFor()
}

// resetNetworks deletes the shared test user's networks through the API.
export async function resetNetworks(request) {
  await request.post('/auth/password', { data: { username: USER, password: PASS } })
  const nets = (await (await request.get('/api/networks')).json()) || []
  for (const n of nets) await request.delete(`/api/networks/${n.id}`)
}

// Confirm the open dialog (typing the confirmation text when asked).
export async function confirm(page, typed) {
  const dlg = page.locator('dialog.confirm[open]')
  if (typed) await dlg.getByRole('textbox').fill(typed)
  await dlg.locator('#dlg-ok').click()
}

// session is an API client signed in as one user: a cookie jar of its own.
export async function session(username, password, baseURL = BASE) {
  const ctx = await pwRequest.newContext({ baseURL })
  const res = await ctx.post('/auth/password', { data: { username, password } })
  if (!res.ok()) throw new Error(`login ${username}: ${res.status()} ${await res.text()}`)
  const call = async (method, path, data) => {
    const r = await ctx.fetch(path, { method, data })
    const body = r.status() === 204 ? null : await r.json().catch(() => null)
    if (!r.ok()) throw new Error(`${method} ${path}: ${r.status()} ${JSON.stringify(body)}`)
    return body
  }
  return { ctx, call, get: (p) => call('GET', p), post: (p, d) => call('POST', p, d), patch: (p, d) => call('PATCH', p, d), del: (p) => call('DELETE', p), dispose: () => ctx.dispose() }
}

let seq = 0
const uniq = (prefix) => `${prefix}${(Date.now() % 1e6).toString(36)}${(seq++).toString(36)}`

// freshUser makes a throwaway account (made by the admin, password already
// changed so there's no forced step) and returns its name, password, id and
// an API session. Call cleanup() when done.
export async function freshUser(prefix = 'u', { admin = false } = {}) {
  const name = uniq(prefix)
  const password = `${name}-secret-pw`
  const a = await session(ADMIN, ADMIN_PASS)
  const temp = 'temporary-pw-1'
  const u = await a.post('/api/admin/users', { username: name, password: temp, role: admin ? 'admin' : 'user' })
  const s = await session(name, temp)
  await s.post('/api/me/password', { current: temp, next: password })
  await s.dispose()
  const api = await session(name, password)
  return {
    name, password, id: u.id, api, admin: a,
    cleanup: async () => { await api.dispose(); await a.del(`/api/admin/users/${u.id}`).catch(() => {}); await a.dispose() },
  }
}

// addNetwork creates a network on the dev ircd through the API and waits for
// it to connect.
export async function addNetwork(api, { name = 'Dev', nick, channels = [] } = {}) {
  const n = await api.post('/api/networks', { name, host: IRCD.host, port: IRCD.port, tls: IRCD.tls, nick, channels })
  await api.post(`/api/networks/${n.id}/connect`)
  await expect.poll(async () => (await api.get('/api/networks')).find((x) => x.id === n.id)?.status, { timeout: 20000 }).toBe('connected')
  return n
}

export { uniq }

// The standalone evrd (evrd-solo) whose IRC connections live in the
// connection holder, and how to run docker compose and evrctl against it.
export const SOLO_URL = process.env.E2E_SOLO_URL || 'http://localhost:8198'
export const COMPOSE = process.env.E2E_COMPOSE || 'docker compose -f ../docker-compose.dev.yml'
export const SOLO_EVRCTL = process.env.E2E_SOLO_EVRCTL ||
  `${COMPOSE} exec -T evrd-solo go run ./cmd/evrctl -config deploy/evrd.solo.conf`

// choose picks an option in one of the app's dropdowns (components/Select):
// open it, then click the option whose text starts with label.
export async function choose(page, combo, label) {
  await combo.click()
  const esc = label.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  await page.getByRole('listbox').getByRole('option', { name: new RegExp(`^${esc}(\\s|$)`) }).click()
  await expect(page.getByRole('listbox')).toHaveCount(0)
}
