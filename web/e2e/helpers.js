export const API = process.env.E2E_API_URL || 'http://localhost:8098'
export const USER = 'e2e'
export const PASS = 'e2epass'

// login drives the UI sign-in form and waits for the dashboard.
export async function login(page) {
  await page.goto('/')
  await page.getByRole('textbox', { name: 'Username' }).fill(USER)
  await page.getByRole('textbox', { name: 'Password' }).fill(PASS)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await page.getByRole('heading', { name: 'Networks' }).waitFor()
}

// resetNetworks deletes all of the test user's networks via the API so each
// test starts from a clean slate.
export async function resetNetworks(request) {
  const res = await request.post(`${API}/api/login`, { data: { username: USER, password: PASS } })
  const { token } = await res.json()
  const headers = { Authorization: `Bearer ${token}` }
  const nets = (await (await request.get(`${API}/api/networks`, { headers })).json()) || []
  for (const n of nets) {
    await request.delete(`${API}/api/networks/${n.id}`, { headers })
  }
}
