import { test, expect } from '@playwright/test'
import { login, freshUser, ADMIN, ADMIN_PASS } from './helpers.js'

// The stack reads its network directory from deploy/networks.dev.json
// (network_directory in deploy/evrd.dev.conf): a few real networks, and the
// stack's own ircd.
const FIXTURE_NETWORKS = 5

let u
test.beforeEach(async ({ page }) => {
  u = await freshUser('dir')
  await login(page, u.name, u.password)
})
test.afterEach(async () => { await u.cleanup() })

const picker = (page) => page.getByTestId('directory')
const entries = (page) => picker(page).getByTestId('directory-network')

test('pick a network from the directory: the form fills in, and it connects', async ({ page }) => {
  await page.goto('/networks/new')
  await expect(entries(page)).toHaveCount(FIXTURE_NETWORKS)

  // search narrows the list
  const search = page.locator('#dir-search')
  await search.fill('libera')
  await expect(entries(page)).toHaveCount(1)
  await expect(entries(page)).toContainText('Libera.Chat')
  await expect(entries(page)).toContainText('irc.libera.chat:+6697')
  await search.fill('nothing like this')
  await expect(entries(page)).toHaveCount(0)
  await expect(picker(page).getByText(/No network matches/)).toBeVisible()

  // picking a real one fills its TLS server and help channel
  await search.fill('libera')
  await entries(page).click()
  await expect(page.getByTestId('directory-picked')).toContainText('Libera.Chat')
  await expect(page.getByRole('textbox', { name: 'Name', exact: true })).toHaveValue('libera')
  await expect(page.locator('#sv-host-0')).toHaveValue('irc.libera.chat')
  await expect(page.locator('#sv-port-0')).toHaveValue('6697')
  await expect(page.getByTestId('server-row')).toHaveCount(1)
  await expect(page.getByTestId('chan-row')).toHaveText([/#libera/])

  // picking another replaces it; Enter in the search picks the first match
  await search.fill('dev stack')
  await search.press('Enter')
  await expect(page.getByTestId('directory-picked')).toContainText('Dev ircd')
  await expect(page.getByRole('textbox', { name: 'Name', exact: true })).toHaveValue('devircd')
  await expect(page.locator('#sv-host-0')).toHaveValue('ircd')
  await expect(page.locator('#sv-port-0')).toHaveValue('6667')
  await expect(page.locator('.server-row').first().getByRole('switch', { name: 'TLS' })).not.toBeChecked()
  await expect(page.getByTestId('chan-row')).toHaveText([/#evrd-dev/])
  // identity is still the user's own
  await expect(page.locator('#nick')).toHaveValue(u.name)

  await page.getByRole('button', { name: 'Add network' }).last().click()
  await expect(page.getByText('Added devircd — connecting…')).toBeVisible()
  await expect(page).toHaveURL(/\/networks\/\d+$/)
  await expect(page.locator('.page-head').getByText('Connected', { exact: true })).toBeVisible({ timeout: 20000 })
  const [n] = await u.api.get('/api/networks')
  expect(n.name).toBe('devircd')
  expect(n.servers).toEqual([expect.objectContaining({ host: 'ircd', port: 6667, tls: false })])
  await expect.poll(async () => (await u.api.get(`/api/networks/${n.id}`)).channels, { timeout: 20000 }).toContain('#evrd-dev')
})

test('enter details manually hides the directory', async ({ page }) => {
  await page.goto('/networks/new')
  await expect(entries(page)).toHaveCount(FIXTURE_NETWORKS)
  await picker(page).getByRole('button', { name: 'Enter details manually' }).click()
  await expect(picker(page)).toHaveCount(0)
  await expect(page.getByRole('textbox', { name: 'Name', exact: true })).toBeFocused()
  await expect(page.locator('#sv-host-0')).toHaveValue('')
})

test('the directory is only on the add page, and the API lists it', async ({ page }) => {
  const d = await u.api.get('/api/directory')
  expect(d.networks).toHaveLength(FIXTURE_NETWORKS)
  expect(d.source).toContain('networks.dev.json')
  const n = await u.api.post('/api/networks', { name: 'Plain', host: 'ircd', port: 6667, tls: false, nick: u.name })
  await page.goto(`/networks/${n.id}`)
  await expect(page.getByRole('heading', { level: 1, name: 'Plain' })).toBeVisible()
  await expect(picker(page)).toHaveCount(0)
})

test('admins see where the directory comes from and can check it again', async ({ browser }) => {
  const page = await browser.newPage()
  try {
    await login(page, ADMIN, ADMIN_PASS)
    await page.goto('/admin/settings')
    const status = page.getByTestId('directory-status')
    await expect(status).toContainText(`${FIXTURE_NETWORKS} networks`)
    await expect(page.getByTestId('setting-network_directory').getByRole('textbox')).toHaveValue(/networks\.dev\.json$/)
    await status.getByRole('button', { name: 'Check now' }).click()
    await expect(page.getByText(`Fetched the directory: ${FIXTURE_NETWORKS} networks`)).toBeVisible()
    await expect(status.getByText('OK', { exact: true })).toBeVisible()
  } finally {
    await page.close()
  }
})
