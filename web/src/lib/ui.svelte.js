// Toasts, the confirm dialog and drawers, rendered once by the layout.
export const toasts = $state([])
let nextId = 1

export function toast(text, kind = 'ok') {
  const id = nextId++
  toasts.push({ id, text, kind, out: false })
  setTimeout(() => {
    const t = toasts.find((x) => x.id === id)
    if (t) t.out = true
    setTimeout(() => {
      const i = toasts.findIndex((x) => x.id === id)
      if (i >= 0) toasts.splice(i, 1)
    }, 160)
  }, 3600)
}

// dialog.current is the open confirm, or null.
export const dialog = $state({ current: null })

// confirmDialog resolves true when the (destructive) action is confirmed.
// typed: text the user must type to enable the action.
export function confirmDialog({ title, body, action = 'Delete', typed = '', danger = true }) {
  return new Promise((resolve) => {
    dialog.current = { title, body, action, typed, danger, resolve }
  })
}

// drawers.open is the id of the open drawer, or ''.
export const drawers = $state({ open: '' })
let lastFocus = null
export function openDrawer(id) {
  lastFocus = document.activeElement
  drawers.open = id
}
export function closeDrawers() {
  drawers.open = ''
  lastFocus?.focus?.()
}

export const reduced = () => typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches
