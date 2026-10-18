export const USER = 'e2e'
export const PASS = 'e2epass1'
export const ADMIN = 'e2eadmin'
export const ADMIN_PASS = 'e2eadminpass'

// login drives the sign-in form and waits for the app.
export async function login(page, user = USER, pass = PASS) {
  await page.goto('/')
  await page.getByRole('textbox', { name: 'Username' }).fill(user)
  await page.getByLabel('Password', { exact: true }).fill(pass)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await page.getByRole('heading', { name: 'Overview' }).waitFor()
}

// resetNetworks deletes the test user's networks through the API so each test
// starts clean. The request context keeps the session cookie.
export async function resetNetworks(request) {
  await request.post('/auth/password', { data: { username: USER, password: PASS } })
  const nets = (await (await request.get('/api/networks')).json()) || []
  for (const n of nets) await request.delete(`/api/networks/${n.id}`)
}

// Confirm the open dialog (typing the confirmation text when asked).
export async function confirm(page, typed) {
  const dlg = page.locator('dialog.modal[open]')
  if (typed) await dlg.getByRole('textbox').fill(typed)
  await dlg.locator('#dlg-ok').click()
}
