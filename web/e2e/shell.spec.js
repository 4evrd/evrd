import { test, expect } from '@playwright/test'
import { login, freshUser, addNetwork, uniq, BASE } from './helpers.js'

let u, net, chan
test.beforeEach(async ({ page }) => {
  u = await freshUser('sh')
  chan = '#' + uniq('s')
  net = await addNetwork(u.api, { name: 'Dev', nick: u.name, channels: [chan] })
  await login(page, u.name, u.password)
})
test.afterEach(async () => { await u.cleanup() })

test('the sidebar reaches every page', async ({ page }) => {
  const nav = page.getByRole('navigation', { name: 'Your bouncer' })
  for (const [link, heading] of [['Networks', 'Networks'], ['IRC clients', 'IRC clients'], ['Overview', 'Overview']]) {
    await nav.getByRole('link', { name: link }).click()
    await expect(page.getByRole('heading', { level: 1, name: heading, exact: true })).toBeVisible()
    await expect(nav.getByRole('link', { name: link })).toHaveAttribute('aria-current', 'page')
  }
  await nav.getByRole('link', { name: /Chat/ }).click()
  await expect(page.locator('#buf-title')).toHaveText(chan)
  await page.getByRole('navigation', { name: 'Account' }).getByRole('link', { name: 'Account' }).click()
  await expect(page.getByRole('heading', { name: 'Account settings' })).toBeVisible()
  await expect(page.getByTestId('side-card')).toContainText(`${u.name}@localhost`)
  await expect(page.getByTestId('side-card')).toContainText('1 of 1 networks up')
  await expect(page.getByRole('navigation', { name: 'Your bouncer' }).getByRole('link', { name: /Networks/ }).locator('.count')).toHaveText('1')
})

test('the sidebar collapses to its icons, and stays so', async ({ page }) => {
  const nav = page.getByRole('navigation', { name: 'Your bouncer' })
  const width = async () => (await page.locator('#sidebar').boundingBox()).width
  expect(await width()).toBeGreaterThan(200)
  await page.getByRole('button', { name: 'Collapse menu' }).click()
  await expect.poll(width).toBeLessThan(100)
  await expect(page.getByTestId('side-card')).toBeHidden()
  // the links keep their names (for screen readers, and as tooltips)
  await expect(nav.getByRole('link', { name: 'Networks' })).toHaveAttribute('title', 'Networks')
  await nav.getByRole('link', { name: 'Networks' }).click()
  await expect(page.getByRole('heading', { level: 1, name: 'Networks', exact: true })).toBeVisible()
  await page.reload()
  await expect.poll(width).toBeLessThan(100)
  await page.getByRole('button', { name: 'Expand menu' }).click()
  await expect.poll(width).toBeGreaterThan(200)
  await expect(page.getByTestId('side-card')).toBeVisible()
})

test('global search jumps to networks and channels', async ({ page }) => {
  const search = page.getByRole('combobox', { name: 'Search networks and channels' })
  await search.fill(chan.slice(1, 5))
  const results = page.getByRole('listbox')
  await expect(results.getByRole('option', { name: new RegExp(chan.slice(1)) })).toBeVisible()
  await search.press('Enter')
  await expect(page.locator('#buf-title')).toHaveText(chan)

  await search.fill('dev')
  await results.getByRole('option', { name: /Dev/ }).first().click()
  await expect(page).toHaveURL(new RegExp(`/networks#net-${net.id}`))

  // nothing matches: Enter searches the networks page
  await search.fill('zzzz')
  await search.press('Enter')
  await expect(page).toHaveURL(/\/networks\?q=zzzz/)
  await expect(page.getByText('No networks match')).toBeVisible()
})

test('⌘K / Ctrl+K focuses search', async ({ page }) => {
  await page.keyboard.press('ControlOrMeta+k')
  await expect(page.locator('#global-search')).toBeFocused()
  await page.goto('/networks')
  await page.locator('h1').click()
  await page.keyboard.press('ControlOrMeta+k')
  // pages with their own search get it instead
  await expect(page.locator('#page-search')).toBeFocused()
})

test('activity shows recent events', async ({ page }) => {
  await page.getByRole('button', { name: 'Activity' }).click()
  const pop = page.getByRole('dialog', { name: 'Recent activity' })
  await expect(pop.getByText('Connected to Dev')).toBeVisible()
  await page.locator('h1').click()
  await expect(pop).toHaveCount(0)
})

test('small screens use a drawer for navigation', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  const burger = page.getByRole('button', { name: 'Open navigation' })
  await expect(burger).toBeVisible()
  await expect(page.locator('#sidebar')).not.toHaveClass(/open/)
  await burger.click()
  await expect(page.locator('#sidebar')).toHaveClass(/open/)
  await page.locator('#sidebar').getByRole('link', { name: 'Networks' }).click()
  await expect(page.getByRole('heading', { level: 1, name: 'Networks' })).toBeVisible()
  await expect(page.locator('#sidebar')).not.toHaveClass(/open/)
  await burger.click()
  await page.keyboard.press('Escape')
  await expect(page.locator('#sidebar')).not.toHaveClass(/open/)
  await expect(burger).toBeFocused()
})

test('a long name shortens in the sidebar’s account card instead of overflowing it', async ({ browser }) => {
  const long = await freshUser('averylongusernameforthe') // 30 characters with the suffix
  const ctx = await browser.newContext({ baseURL: BASE })
  const page = await ctx.newPage()
  try {
    await login(page, long.name, long.password)
    const card = page.getByTestId('side-card')
    await expect(card).toContainText(long.name.slice(0, 12))
    const fits = await page.evaluate(() => {
      const foot = document.querySelector('.side-foot').getBoundingClientRect()
      const card = document.querySelector('[data-testid=side-card]').getBoundingClientRect()
      return card.right <= foot.right + 0.5
    })
    expect(fits).toBe(true)
  } finally { await ctx.close(); await long.cleanup() }
})
