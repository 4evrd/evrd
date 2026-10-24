import { readFileSync } from 'node:fs'
import { test, expect } from '@playwright/test'
import { login, freshUser, choose } from './helpers.js'

// Importing networks: a ZNC config (pick the user, then the networks), and
// an evrd export carried to another account.

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
