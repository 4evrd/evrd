import { writable } from 'svelte/store'
import { api } from '../api/client.js'

// authBloc — owns the signed-in session. State out via stores, intent in via
// methods.
export function createAuthBloc() {
  const session = writable(api.session())
  const error = writable('')
  const busy = writable(false)

  async function login(username, password) {
    busy.set(true)
    error.set('')
    try {
      session.set(await api.login(username, password))
    } catch (e) {
      error.set(e.message)
    } finally {
      busy.set(false)
    }
  }

  function logout() {
    api.logout()
    session.set(null)
  }

  return { session, error, busy, login, logout }
}
