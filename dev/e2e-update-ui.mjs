// The browser half of dev/e2e-update.sh: an admin signs in, sees the new
// release on Admin, clicks Install, confirms, and the web app comes back on
// the new version. WEB_BASE, ADMIN, ADMIN_PASS; SHOT: a screenshot path.
import { createRequire } from 'node:module'

const require = createRequire(new URL('../web/package.json', import.meta.url))
const { chromium } = require('@playwright/test')

const base = process.env.WEB_BASE
const browser = await chromium.launch()
const page = await browser.newPage({ baseURL: base })
try {
  await page.goto('/')
  await page.getByRole('textbox', { name: 'Username' }).fill(process.env.ADMIN)
  await page.getByLabel('Password', { exact: true }).fill(process.env.ADMIN_PASS)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await page.getByRole('heading', { name: 'Overview', exact: true }).waitFor()

  // Settings → Updates: the full panel
  await page.goto('/admin/settings')
  const upd = page.locator('section', { has: page.getByRole('heading', { name: 'Updates' }) })
  await upd.getByTestId('update-status').getByText('evrd 0.2.0 is out').waitFor()
  await upd.getByRole('button', { name: 'Check now' }).waitFor()
  if (process.env.SHOT) await upd.screenshot({ path: process.env.SHOT.replace('.png', '-settings.png') })

  await page.goto('/admin')
  const panel = page.getByTestId('update-status')
  await panel.getByText('evrd 0.2.0 is out').waitFor()
  await panel.getByText('This server runs 0.1.0').waitFor()
  if (process.env.SHOT) await page.screenshot({ path: process.env.SHOT.replace('.png', '-before.png') })
  await panel.getByTestId('update-install').click()
  const dlg = page.locator('dialog.confirm[open]')
  await dlg.getByText('Install evrd 0.2.0?').waitFor()
  await dlg.locator('#dlg-ok').click()
  await page.getByText('Now running evrd 0.2.0').waitFor({ timeout: 120_000 })
  await page.waitForEvent('load', { timeout: 30_000 }) // reloads into the new web app
  await page.getByRole('heading', { name: 'Bouncer overview' }).waitFor()
  // nothing more to install
  if (await page.getByTestId('update-status').count()) throw new Error('the update is still offered after installing it')
  if (process.env.SHOT) await page.screenshot({ path: process.env.SHOT })
} catch (e) {
  if (process.env.SHOT) await page.screenshot({ path: process.env.SHOT.replace('.png', '-failed.png') }).catch(() => {})
  console.error(e)
  process.exitCode = 1
} finally {
  await browser.close()
}
