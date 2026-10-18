import { test, expect } from '@playwright/test'
import { ADMIN, ADMIN_PASS, login, confirm, session, freshUser } from './helpers.js'

// Server settings are global: every test puts back what it changed.
const restore = async (keys) => {
  const a = await session(ADMIN, ADMIN_PASS)
  await a.patch('/api/admin/settings', { set: {}, reset: keys })
  await a.dispose()
}

test('an admin changes server settings; they apply live and can be reset', async ({ page }) => {
  const u = await freshUser('st')
  try {
    await login(page, ADMIN, ADMIN_PASS)
    await page.getByRole('link', { name: 'Settings' }).click()
    await expect(page.getByRole('heading', { level: 1, name: 'Settings' })).toBeVisible()
    const host = page.getByTestId('setting-irc_host')
    await expect(host.getByText('evrd.conf', { exact: true })).toBeVisible()

    await host.getByRole('textbox').fill('irc.e2e.example')
    await page.getByTestId('setting-max_networks').getByRole('textbox').fill('1')
    await page.getByTestId('setting-session_ttl').getByRole('textbox').fill('soon')
    await expect(page.getByText('3 changes')).toBeVisible()
    await page.getByRole('button', { name: 'Save changes' }).click()
    await expect(page.getByText(/A duration like 720h/)).toBeVisible()
    await page.getByTestId('setting-session_ttl').getByRole('textbox').fill('720h')
    await page.getByRole('button', { name: 'Save changes' }).click()
    await expect(page.getByText('Saved — applied on every node')).toBeVisible()
    await expect(host.getByText('Set here')).toBeVisible()
    await expect(host.getByText(/by e2eadmin/)).toBeVisible()

    // live: the IRC clients page and the user's limit follow at once
    await page.goto('/clients')
    await expect(page.getByTestId('irc-host')).toHaveText('irc.e2e.example')
    await expect.poll(async () => (await u.api.get('/api/me')).limits.maxNetworks).toBe(1)

    // restart-only settings say so
    await page.goto('/admin/settings')
    const pub = page.getByTestId('setting-public_url')
    await pub.getByRole('textbox').fill('http://bnc.e2e.example')
    await page.getByRole('button', { name: 'Save changes' }).click()
    await expect(page.getByText(/Public URL takes effect after a restart/)).toBeVisible()
    await expect(pub.getByText('Restart needed')).toBeVisible()

    // reset puts back evrd.conf's value
    await host.getByRole('button', { name: 'Reset' }).click()
    await expect(page.locator('dialog.confirm[open]')).toContainText('evrd.conf’s value (localhost)')
    await confirm(page)
    await expect(host.getByRole('textbox')).toHaveValue('localhost')
    await expect(host.getByText('evrd.conf', { exact: true })).toBeVisible()

    // startup settings are shown, not editable
    await expect(page.getByRole('heading', { name: /Startup/ })).toBeVisible()
    await expect(page.locator('.kv', { hasText: 'secret_key' })).toContainText('set')
    await expect(page.locator('.kv', { hasText: /^db/ }).first()).toContainText('•••')
  } finally {
    await restore(['irc_host', 'max_networks', 'session_ttl', 'public_url'])
    await u.cleanup()
  }
})

test('users can’t see or change server settings', async ({ page }) => {
  const u = await freshUser('nst')
  try {
    await expect(u.api.get('/api/admin/settings')).rejects.toThrow(/403/)
    await expect(u.api.patch('/api/admin/settings', { set: { max_networks: '99' } })).rejects.toThrow(/403/)
    await login(page, u.name, u.password)
    await expect(page.getByRole('link', { name: 'Settings' })).toHaveCount(0)
    await page.goto('/admin/settings')
    await expect(page.getByText('Admins only.')).toBeVisible()
  } finally { await u.cleanup() }
})

test('HTTPS & TLS: who serves HTTPS, the certificate, and fields for the source picked', async ({ page }) => {
  try {
    await login(page, ADMIN, ADMIN_PASS)
    await page.goto('/admin/settings')
    // the dev stack serves HTTPS through nginx, not evrd
    await expect(page.getByTestId('tls-who')).toContainText('A proxy in front of evrd handles HTTPS')
    await expect(page.getByTestId('tls-certs')).toContainText('self-signed')

    const source = page.getByTestId('setting-tls').getByRole('combobox')
    await expect(source).toHaveValue('selfsigned')
    await expect(page.getByTestId('setting-acme_email')).toHaveCount(0)
    await source.selectOption('acme')
    await expect(page.getByTestId('setting-acme_email')).toBeVisible()
    await expect(page.getByTestId('setting-tls_cert')).toHaveCount(0)

    // localhost can't get a Let's Encrypt certificate: refused, not saved
    await page.getByRole('button', { name: 'Save changes' }).click()
    await expect(page.getByText(/needs a public host name/)).toBeVisible()
    await source.selectOption('files')
    await expect(page.getByTestId('setting-tls_cert')).toBeVisible()
    await page.getByRole('button', { name: 'Discard' }).click()
    await expect(source).toHaveValue('selfsigned')
  } finally {
    await restore(['tls'])
  }
})

test('password sign-in can’t be turned off without single sign-on', async ({ page }) => {
  await login(page, ADMIN, ADMIN_PASS)
  await page.goto('/admin/settings')
  await page.getByTestId('setting-password_login').getByRole('switch').click()
  await page.getByRole('button', { name: 'Save changes' }).click()
  await expect(page.getByText(/set up single sign-on before turning off password sign-in/)).toBeVisible()
  // nothing changed: password sign-in still works
  await page.reload()
  await expect(page.getByTestId('setting-password_login').getByRole('switch')).toBeChecked()
  await (await session(ADMIN, ADMIN_PASS)).dispose()
})
