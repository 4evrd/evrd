import { test, expect } from '@playwright/test'
import { IRC } from './irc.js'
import { freshUser, addNetwork, login, uniq } from './helpers.js'

// IRC sign-in by link (#91): a client without a password gets a link; its
// owner signs in on the website and allows it, remembering the device with
// a client password that signs it in by itself next time.

test('an IRC client without a password is allowed by link', async ({ page }) => {
  const u = await freshUser('lnk')
  const chan = '#' + uniq('lnk')
  await addNetwork(u.api, { name: 'Dev', nick: u.name, channels: [chan] })
  const clients = []
  try {
    // "user/network:" and no password: a link to the website
    let irc = await IRC.connect({ user: u.name, network: 'Dev', password: '' })
    clients.push(irc)
    const line = await irc.waitFor(/Sign in on the website to connect: (\S+)/, 15000)
    const link = new URL(line.match(/Sign in on the website to connect: (\S+)/)[1])
    expect(link.pathname).toMatch(/^\/irc\/approve\/[A-Za-z0-9_-]{40,}$/)

    // the page asks to sign in first, then shows the client
    await page.goto(link.pathname)
    await page.getByRole('textbox', { name: 'Username' }).fill(u.name)
    await page.getByLabel('Password', { exact: true }).fill(u.password)
    await page.getByRole('button', { name: 'Sign in' }).click()
    await expect(page.getByRole('heading', { name: 'Allow an IRC sign-in' })).toBeVisible()
    await expect(page.getByTestId('signin-nick')).toHaveText(u.name)
    await expect(page.getByText('Only allow it if you just connected an IRC client yourself.')).toBeVisible()
    await page.getByRole('button', { name: 'Allow' }).click()
    await expect(page.getByTestId('signin-allowed')).toBeVisible()
    const shown = await page.getByTestId('signin-password').locator('.code').innerText()
    const secret = shown.slice(shown.lastIndexOf(':') + 1)
    await irc.waitFor(/Allowed on the website/, 15000)
    await irc.waitFor(new RegExp(` JOIN :?${chan}`), 15000)

    // the link is used up
    await page.goto(link.pathname)
    await expect(page.getByTestId('signin-gone')).toBeVisible()

    // remembered: the device's password signs in by itself, and shows on the clients page
    irc = await IRC.connect({ user: u.name, network: 'Dev', password: secret })
    clients.push(irc)
    await irc.waitFor(new RegExp(` JOIN :?${chan}`), 15000)
    await page.goto('/clients')
    await expect(page.getByTestId('remembered')).toContainText('remembered until')

    // turned away: the client is closed
    irc = await IRC.connect({ user: u.name, network: 'Dev', password: '' })
    clients.push(irc)
    const again = await irc.waitFor(/Sign in on the website to connect: (\S+)/, 15000)
    await page.goto(new URL(again.match(/connect: (\S+)/)[1]).pathname)
    await page.getByRole('button', { name: 'Turn away' }).click()
    await expect(page.getByTestId('signin-denied')).toBeVisible()
    await irc.waitFor(/ERROR :The sign-in was turned away on the website/, 15000)
  } finally {
    for (const c of clients) c.close()
    await u.cleanup()
  }
})

test('a link for someone else can only be turned away', async ({ page }) => {
  const u = await freshUser('lnk')
  const other = await freshUser('lnk')
  const clients = []
  try {
    const irc = await IRC.connect({ user: other.name, network: 'Dev', password: '' })
    clients.push(irc)
    const line = await irc.waitFor(/Sign in on the website to connect: (\S+)/, 15000)
    await login(page, u.name, u.password)
    await page.goto(new URL(line.match(/connect: (\S+)/)[1]).pathname)
    await expect(page.getByTestId('signin-not-yours')).toContainText(other.name)
    await expect(page.getByRole('button', { name: 'Allow' })).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'Turn away' })).toBeVisible()
  } finally {
    for (const c of clients) c.close()
    await other.cleanup()
    await u.cleanup()
  }
})
