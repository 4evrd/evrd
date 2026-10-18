// Client-side checks that mirror the daemon's rules so mistakes show inline.
// The daemon stays the authority; its error is shown when it disagrees.
export const USERNAME_RE = /^[a-z][a-z0-9_-]{1,31}$/
export const HOST_RE = /^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$/i

export function usernameError(v) {
  if (!v) return 'Enter a username.'
  if (!USERNAME_RE.test(v)) return 'Use 2–32 characters: a–z, 0–9, - and _, starting with a letter.'
  return ''
}

export function passwordError(v, { optional = false } = {}) {
  if (!v) return optional ? '' : 'Enter a password.'
  if ([...v].length < 8) return `Use at least 8 characters (${[...v].length} so far).`
  if (new TextEncoder().encode(v).length > 72) return 'Passwords are limited to 72 bytes.'
  return ''
}

export function nickError(v, { optional = false } = {}) {
  if (!v) return optional ? '' : 'Enter a nick.'
  if (/[\s,*?!@#:]/.test(v)) return 'Nicks can’t contain spaces or , * ? ! @ # :'
  if (v.length > 30) return 'Keep it to 30 characters.'
  return ''
}

export function hostError(v) {
  if (!v) return 'Enter the server, e.g. irc.libera.chat.'
  if (!HOST_RE.test(v)) return 'That doesn’t look like a hostname.'
  return ''
}

export function portError(v) {
  const n = Number(v)
  if (!Number.isInteger(n) || n < 1 || n > 65535) return 'Ports are 1–65535.'
  return ''
}

export function channelsError(list) {
  const bad = list.find((c) => !/^[#&+!][^\s,\x07]{1,49}$/.test(c))
  return bad ? `“${bad}” isn’t a channel name — channels start with #.` : ''
}

// limit input: '' = server default, otherwise a whole number ≥ 0
export function limitError(v) {
  if (v === '' || v == null) return ''
  const n = Number(v)
  if (!Number.isInteger(n) || n < 0) return 'A whole number, 0 for unlimited — or empty for the default.'
  return ''
}

// onblur handler that validates — unless the user is pressing the form's
// submit button. An error appearing on blur shifts the layout, so the click
// would land on empty space and the form would never submit; submitting
// validates every field anyway. Safari doesn't focus buttons on click, so the
// pointer press is tracked as well as relatedTarget.
let pressed = null
if (typeof document !== 'undefined') {
  document.addEventListener('pointerdown', (e) => { pressed = e.target.closest?.('button[type=submit]') || null }, true)
  document.addEventListener('click', () => { pressed = null }, true)
}
export const onBlurCheck = (fn) => (e) => {
  const form = e.target.form
  if (e.relatedTarget?.type === 'submit' && e.relatedTarget.form === form) return
  if (pressed && pressed.form === form) return
  fn()
}
