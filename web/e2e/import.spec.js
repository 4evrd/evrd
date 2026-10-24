import { readFileSync } from 'node:fs'
import { test, expect } from '@playwright/test'
import { login, freshUser, choose, session, uniq, ADMIN, ADMIN_PASS } from './helpers.js'

// Importing networks: a ZNC config (pick the user, then the networks), an
// evrd export carried to another account, and an admin bringing a whole ZNC
// over.

const conf = `Version = 1.8.2
<User first>
	Nick = firstnick
	<Network elsewhere>
		Server = irc.example.invalid +6697
	</Network>
</User>
<User second>
	Nick = secondnick
	<Network devirc>
		Server = ircd 6667
		LoadModule = nickserv
		<Chan #imported>
		</Chan>
	</Network>
	<Network skipme>
		Server = ircd 6667
	</Network>
</User>
`

let users = []
test.afterEach(async () => {
  for (const u of users) await u.cleanup()
  users = []
})

test('a ZNC config: pick the user and a network, it imports and connects', async ({ page }) => {
  const u = await freshUser('imp')
  users.push(u)
  await login(page, u.name, u.password)
  await page.goto('/networks')
  await page.getByTestId('import-link').click()
  await expect(page).toHaveURL(/\/networks\/import$/)
  await page.getByTestId('import-paste').fill(conf)
  await page.getByTestId('import-read').click()
  await expect(page.getByTestId('import-preview')).toContainText('ZNC config')
  await expect(page.getByTestId('import-network')).toHaveCount(1) // first's
  await choose(page, page.getByTestId('import-user'), 'second')
  const rows = page.getByTestId('import-network')
  await expect(rows).toHaveCount(2)
  await expect(rows.first()).toContainText('ircd:6667 · secondnick · 1 channel')
  await expect(rows.first()).toContainText('nickserv module keeps its password outside the config')
  await rows.nth(1).getByRole('checkbox').uncheck()
  await page.getByRole('switch', { name: 'Connect them once imported' }).click()
  await page.getByTestId('import-go').click()
  await expect(page.getByTestId('import-result')).toHaveCount(1)
  await expect(page.getByTestId('import-result')).toHaveAttribute('data-result', 'created')

  await page.getByRole('link', { name: 'Go to your networks' }).click()
  const card = page.getByTestId('network-row')
  await expect(card).toHaveCount(1)
  await expect(card).toContainText('devirc')
  await expect(card).toContainText('Connected', { timeout: 20_000 })
})

test('an export imports into another account; again, the network is skipped', async ({ page }) => {
  const a = await freshUser('exp')
  const b = await freshUser('imp')
  users.push(a, b)
  await login(page, a.name, a.password)
  await page.goto('/networks/import')
  await page.getByTestId('import-paste').fill(conf)
  await page.getByTestId('import-read').click()
  await choose(page, page.getByTestId('import-user'), 'second')
  await page.getByTestId('import-go').click()
  await expect(page.getByTestId('import-result')).toHaveCount(2)

  await page.goto('/networks')
  const [download] = await Promise.all([page.waitForEvent('download'), page.getByTestId('export').click()])
  expect(download.suggestedFilename()).toMatch(/^evrd-networks-.*\.yaml$/)
  const file = readFileSync(await download.path(), 'utf8')
  expect(file).toContain('evrd_networks: 1')
  expect(file).toContain('#imported')

  await page.context().clearCookies()
  await login(page, b.name, b.password)
  await page.goto('/networks/import')
  await page.getByTestId('import-paste').fill(file)
  await page.getByTestId('import-read').click()
  await expect(page.getByTestId('import-preview')).toContainText('evrd networks file')
  await expect(page.getByTestId('import-user')).toHaveCount(0)
  await page.getByTestId('import-go').click()
  await expect(page.locator('[data-testid=import-result][data-result=created]')).toHaveCount(2)

  await page.getByRole('button', { name: 'Use another file' }).click()
  await page.getByTestId('import-paste').fill(file)
  await page.getByTestId('import-read').click()
  await expect(page.getByTestId('import-network').first()).toContainText('Already added')
  await expect(page.getByTestId('import-go')).toBeDisabled()
})

test('an admin brings a ZNC over: new accounts keep their ZNC password', async ({ page }) => {
  const a = await session(ADMIN, ADMIN_PASS)
  const one = uniq('zone'), two = uniq('ztwo')
  try {
    const zconf = `<User ${one}>\n Pass = plain#first-znc-password\n <Network devirc>\n  Server = ircd 6667\n  <Chan #fromznc>\n  </Chan>\n </Network>\n</User>\n` +
      `<User ${two}>\n <Network devirc>\n  Server = ircd 6667\n </Network>\n <Network other>\n  Server = ircd 6667\n </Network>\n</User>\n`
    await login(page, ADMIN, ADMIN_PASS)
    await page.goto('/admin/users')
    await page.getByTestId('znc-import-link').click()
    await page.getByTestId('znc-paste').fill(zconf)
    await page.getByTestId('znc-read').click()
    const users = page.getByTestId('znc-user')
    await expect(users).toHaveCount(2)
    await expect(users.first()).toContainText('New account')
    await expect(users.first()).toContainText('Keeps their ZNC password')
    await expect(users.nth(1)).toContainText('No password in the config')
    await users.nth(1).getByTestId('znc-email').fill(`${two}@example.org`)
    await users.nth(1).locator('label.pick').filter({ hasText: 'other' }).getByRole('checkbox').uncheck()
    await expect(page.getByTestId('znc-go')).toHaveText(/Bring 2 users over \(2 networks\)/)
    await page.getByTestId('znc-go').click()
    const res = page.getByTestId('znc-result')
    await expect(res).toHaveCount(2)
    await expect(res.first()).toContainText('Account made')
    await expect(res.first()).toContainText('ZNC password kept')
    await expect(res.nth(1)).toContainText('claimable by SSO')
    await expect(page.locator('[data-testid=znc-result] [data-result=created]')).toHaveCount(2)

    const s = await session(one, 'first-znc-password')
    const nets = await s.get('/api/networks')
    expect(nets.map((n) => [n.name, n.channels])).toEqual([['devirc', ['#fromznc']]])
    await s.dispose()
  } finally {
    for (const u of (await a.get('/api/admin/users')).filter((u) => [one, two].includes(u.username))) await a.del(`/api/admin/users/${u.id}`).catch(() => {})
    await a.dispose()
  }
})
