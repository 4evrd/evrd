import { test, expect } from '@playwright/test'
import tls from 'node:tls'
import { execFileSync } from 'node:child_process'
import { mkdtempSync, readFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { X509Certificate } from 'node:crypto'
import { IRC } from './irc.js'
import { freshUser, addNetwork, login, confirm, uniq } from './helpers.js'

// Signing IRC clients in with a TLS client certificate (certauth): add its
// fingerprint on Account → Security (or the one an attached client used),
// then a client presenting it over TLS signs in without a password — with
// no PASS at all, or with SASL EXTERNAL. A certificate that isn't on the
// account gets nowhere.

const HOST = process.env.E2E_IRC_HOST || 'localhost'
const TLS_PORT = +(process.env.E2E_IRC_TLS_PORT || 6697)

let dir
test.beforeAll(() => { dir = mkdtempSync(join(tmpdir(), 'evrd-certauth-')) })
test.afterAll(() => rmSync(dir, { recursive: true, force: true }))

// makeCert is a self-signed client certificate, made the way the docs say
// (docs/irc-clients.md), with its SHA-256 fingerprint as openssl shows it
// (AB:CD:…).
function makeCert(name) {
  const key = join(dir, `${name}.key`)
  const crt = join(dir, `${name}.crt`)
  execFileSync('openssl', ['req', '-x509', '-newkey', 'ec', '-pkeyopt', 'ec_paramgen_curve:prime256v1', '-nodes',
    '-keyout', key, '-out', crt, '-days', '2', '-subj', `/CN=${name}`], { stdio: 'pipe' })
  const cert = readFileSync(crt)
  return { cert, key: readFileSync(key), fingerprint: new X509Certificate(cert).fingerprint256 }
}

const hex = (fp) => fp.replace(/:/g, '').toLowerCase()

// connectTLS is an IRC client (irc.js's, for its waitFor) on evrd's TLS
// port, presenting cert if given; the test sends registration itself.
function connectTLS(cert) {
  const c = new IRC()
  c.version = 'e2e-certauth 1.0'
  return new Promise((resolve, reject) => {
    const opts = { host: HOST, port: TLS_PORT, servername: HOST, rejectUnauthorized: false } // the dev stack's cert is self-signed
    if (cert) Object.assign(opts, { cert: cert.cert, key: cert.key })
    c.sock = tls.connect(opts, () => resolve(c))
    c.sock.on('error', reject)
    let buf = ''
    c.sock.on('data', (d) => {
      buf += d.toString('utf8')
      let i
      while ((i = buf.indexOf('\r\n')) >= 0) {
        const line = buf.slice(0, i)
        buf = buf.slice(i + 2)
        c.lines.push(line)
        if (line.startsWith('PING ')) c.send('PONG ' + line.slice(5))
        const v = line.match(/^:(\S+?)!\S+ PRIVMSG \S+ :\x01VERSION\x01$/)
        if (v) c.send(`NOTICE ${v[1]} :\x01VERSION ${c.version}\x01`)
        for (const w of [...c.waiters]) if (w.re.test(line)) { c.waiters.delete(w); w.resolve(line) }
      }
    })
  })
}

// capLS asks for the capabilities (registering as nick meanwhile, as
// clients do) and returns what evrd offers, every line of it.
async function capLS(c, nick) {
  c.send('CAP LS 302')
  c.send(`NICK ${nick}`)
  c.send(`USER ${nick} 0 * :${nick}`)
  await c.waitFor(/ CAP \S+ LS :/, 15000) // the last (or only) line
  return c.lines.filter((l) => / CAP \S+ LS (\* )?:/.test(l)).join(' ')
}

// startExternal asks for SASL EXTERNAL; authzid names the account ("" for
// whichever the certificate is on).
async function startExternal(c, nick, authzid = '') {
  expect(await capLS(c, nick)).toMatch(/sasl=[A-Z,]*EXTERNAL/)
  c.send('CAP REQ :sasl')
  await c.waitFor(/ CAP \S+ ACK :sasl/)
  c.send('AUTHENTICATE EXTERNAL')
  await c.waitFor(/^AUTHENTICATE \+$/)
  c.send('AUTHENTICATE ' + (authzid ? Buffer.from(authzid).toString('base64') : '+'))
}

// external signs in with SASL EXTERNAL: no password, the certificate says who.
async function external(c, nick) {
  await startExternal(c, nick)
  await c.waitFor(/ 903 /)
  c.send('CAP END')
}

// The dev stack has sign-in by link on: a client evrd can't sign in is
// sent a link to sign in on the website, and put on no network.
const LINK = /Sign in on the website to connect/

test('a client certificate added on the Account page signs IRC clients in without a password', async ({ page }) => {
  const u = await freshUser('cert')
  const chan = '#' + uniq('cert')
  await addNetwork(u.api, { name: 'Dev', nick: u.name, channels: [chan] })
  const phone = makeCert('phone')
  const laptop = makeCert('laptop')
  const clients = []
  try {
    // a client signed in with the password, presenting a certificate: the
    // Account page offers to add it
    let irc = await connectTLS(phone)
    clients.push(irc)
    irc.send(`PASS ${u.name}/Dev:${u.password}`)
    irc.send(`NICK ${u.name}`)
    irc.send(`USER ${u.name} 0 * :${u.name}`)
    await irc.waitFor(new RegExp(` JOIN :?${chan}`), 15000)

    await login(page, u.name, u.password)
    await page.goto('/account#security')
    const box = page.getByTestId('client-certs')
    await expect(box.getByTestId('certs-none')).toBeVisible()
    const offer = box.getByTestId('cert-offer')
    await expect(offer).toContainText(phone.fingerprint)
    await offer.getByRole('button', { name: 'Add this certificate' }).click()
    await expect(box.getByTestId('cert-row')).toHaveCount(1)
    await expect(box.getByTestId('cert-row')).toContainText(phone.fingerprint)
    await expect(offer).toHaveCount(0)
    irc.close()

    // or by its fingerprint, as openssl shows it, with a label
    await box.getByLabel('SHA-256 fingerprint').fill(laptop.fingerprint)
    await box.getByLabel('Label', { exact: true }).fill('laptop')
    await box.getByRole('button', { name: 'Add certificate' }).click()
    await expect(box.getByTestId('cert-row')).toHaveCount(2)
    await expect(box.getByTestId('cert-row').filter({ hasText: 'laptop' })).toContainText(laptop.fingerprint)
    // the same one twice: refused
    await box.getByLabel('SHA-256 fingerprint').fill(hex(laptop.fingerprint))
    await box.getByRole('button', { name: 'Add certificate' }).click()
    await expect(box.locator('#cert-fp-err')).toContainText('already on your account')

    // no PASS at all: USER user/network picks the network
    irc = await connectTLS(phone)
    clients.push(irc)
    irc.send(`NICK ${u.name}`)
    irc.send(`USER ${u.name}/Dev 0 * :${u.name}`)
    await irc.waitFor(/ 001 /, 15000)
    await irc.waitFor(new RegExp(` JOIN :?${chan}`), 15000)
    expect(irc.lines.some((l) => / 464 /.test(l))).toBe(false)

    // SASL EXTERNAL
    irc = await connectTLS(laptop)
    clients.push(irc)
    await external(irc, u.name)
    await irc.waitFor(/ 001 /, 15000)
    await irc.waitFor(new RegExp(` JOIN :?${chan}`), 15000)

    // *status lists them, and knows this client's
    irc.send('PRIVMSG *certauth :list')
    await irc.waitFor(new RegExp(`laptop`))
    await irc.waitFor(new RegExp(`This client's certificate: ${hex(laptop.fingerprint)}`))

    // removed on the page: that certificate signs no one in any more
    for (const c of clients) c.close()
    await page.reload()
    const row = box.getByTestId('cert-row').filter({ hasText: phone.fingerprint })
    await row.getByRole('button', { name: /^Remove/ }).click()
    await confirm(page)
    await expect(box.getByTestId('cert-row')).toHaveCount(1)
    irc = await connectTLS(phone)
    clients.push(irc)
    irc.send(`NICK ${u.name}`)
    irc.send(`USER ${u.name}/Dev 0 * :${u.name}`)
    await irc.waitFor(/NOTICE \* :The certificate this client sent .* isn't on the account/, 15000)
    await irc.waitFor(LINK, 15000)
    expect(irc.lines.some((l) => / JOIN /.test(l))).toBe(false)
  } finally {
    for (const c of clients) c.close?.()
    await u.cleanup()
  }
})

test('a certificate that is not on the account is refused', async () => {
  const u = await freshUser('certno')
  const chan = '#' + uniq('certno')
  await addNetwork(u.api, { name: 'Dev', nick: u.name, channels: [chan] })
  const mine = makeCert('mine')
  const stranger = makeCert('stranger')
  const clients = []
  const onNetwork = (c) => c.lines.some((l) => new RegExp(` JOIN :?${chan}`).test(l))
  try {
    await u.api.post('/api/me/client-certs', { fingerprint: mine.fingerprint, label: 'mine' })

    // no PASS: told the certificate isn't known, and not signed in
    let irc = await connectTLS(stranger)
    clients.push(irc)
    irc.send(`NICK ${u.name}`)
    irc.send(`USER ${u.name}/Dev 0 * :${u.name}`)
    await irc.waitFor(new RegExp(`NOTICE \\* :The certificate this client sent \\(SHA-256 ${hex(stranger.fingerprint)}\\) isn't on the account`), 15000)
    await irc.waitFor(LINK, 15000)
    expect(onNetwork(irc)).toBe(false)

    // SASL EXTERNAL naming the account: 904, and still not signed in
    irc = await connectTLS(stranger)
    clients.push(irc)
    await startExternal(irc, u.name, u.name)
    await irc.waitFor(/ 904 \S+ :The certificate this client sent/)
    irc.send('CAP END')
    await irc.waitFor(LINK, 15000)
    expect(onNetwork(irc)).toBe(false)

    // no certificate at all: EXTERNAL isn't offered
    irc = await connectTLS(null)
    clients.push(irc)
    expect(await capLS(irc, u.name)).not.toContain('EXTERNAL')

    // the right one still works
    irc = await connectTLS(mine)
    clients.push(irc)
    irc.send(`NICK ${u.name}`)
    irc.send(`USER ${u.name}/Dev 0 * :${u.name}`)
    await irc.waitFor(new RegExp(` JOIN :?${chan}`), 15000)
  } finally {
    for (const c of clients) c.close?.()
    await u.cleanup()
  }
})
