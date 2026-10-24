import { test, expect } from '@playwright/test'
import { createHmac } from 'node:crypto'
import { IRC } from './irc.js'
import { freshUser, addNetwork, login, uniq } from './helpers.js'

// A second factor for IRC sign-ins (#91): set up an authenticator app on
// Account → Security, then an IRC client signing in with the password is
// asked for a code (or sends it after the password).

// totp is RFC 6238's code for a base32 secret, steps from now.
function totp(secret, steps = 0) {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'
  let bits = ''
  for (const ch of secret.replace(/\s/g, '').toUpperCase()) bits += alphabet.indexOf(ch).toString(2).padStart(5, '0')
  const key = Buffer.from(bits.match(/.{8}/g).map((b) => parseInt(b, 2)))
  const counter = Buffer.alloc(8)
  counter.writeBigUInt64BE(BigInt(Math.floor(Date.now() / 30000) + steps))
  const h = createHmac('sha1', key).update(counter).digest()
  const off = h[h.length - 1] & 15
  return String((h.readUInt32BE(off) & 0x7fffffff) % 1000000).padStart(6, '0')
}

test('an authenticator app guards IRC sign-ins with the password', async ({ page }) => {
  const u = await freshUser('mfa')
  const chan = '#' + uniq('mfa')
  await addNetwork(u.api, { name: 'Dev', nick: u.name, channels: [chan] })
  const clients = []
  try {
    await login(page, u.name, u.password)
    await page.goto('/account#security')
    const box = page.getByTestId('second-factor')
    await expect(box.getByRole('switch')).toBeDisabled() // no app yet
    await box.getByRole('button', { name: 'Set up an authenticator app' }).click()
    await expect(box.getByRole('img', { name: 'QR code for your authenticator app' })).toBeVisible()
    const secret = await box.getByTestId('mfa-secret').innerText()
    await box.getByLabel('The code it shows').fill(totp(secret, -1))
    await box.getByRole('button', { name: 'Turn on' }).click()
    await expect(box.getByTestId('recovery-codes').locator('li')).toHaveCount(10)
    const codes = await box.getByTestId('recovery-codes').locator('li').allInnerTexts()
    expect(codes).toHaveLength(10)
    await box.getByRole('button', { name: "I've saved them" }).click()
    await expect(box.getByTestId('mfa-status')).toContainText('10 recovery codes left')
    await box.getByRole('switch').click()
    await expect(box.getByRole('switch')).toHaveAttribute('aria-checked', 'true')

    // asked for a code: on no network until it's in
    let irc = await IRC.connect({ user: u.name, network: 'Dev', password: u.password })
    clients.push(irc)
    await irc.waitFor(/A second factor is needed before you're on Dev/, 15000)
    irc.send(`JOIN ${chan}`)
    await irc.waitFor(/Send your second factor first/)
    irc.send(`PRIVMSG *status :otp ${totp(secret)}`)
    await irc.waitFor(/Second factor accepted/)
    await irc.waitFor(new RegExp(` JOIN :?${chan}`), 15000)

    // or sent after the password: a recovery code, once
    irc = await IRC.connect({ user: u.name, network: 'Dev', password: `${u.password}:${codes[0]}` })
    clients.push(irc)
    await irc.waitFor(new RegExp(` JOIN :?${chan}`), 15000)
    irc = await IRC.connect({ user: u.name, network: 'Dev', password: `${u.password}:${codes[0]}` })
    clients.push(irc)
    await irc.waitFor(/464 \S+ :Wrong second-factor code/, 15000)

    await page.reload()
    await expect(page.getByTestId('second-factor').getByTestId('mfa-status')).toContainText('9 recovery codes left')
  } finally {
    for (const c of clients) c.close?.()
    await u.cleanup()
  }
})
