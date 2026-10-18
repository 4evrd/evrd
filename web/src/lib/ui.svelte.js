// Toasts and the confirm dialog, rendered once by the layout.
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
  }, 4000)
}

// dialog.current is the open confirm, or null. body is trusted HTML built by
// the caller with esc() around any user data.
export const dialog = $state({ current: null })

export function confirmDialog({ title, body, confirmLabel = 'Confirm', danger = false, requireText = '' }) {
  return new Promise((resolve) => {
    dialog.current = { title, body, confirmLabel, danger, requireText, resolve }
  })
}

export const esc = (s) => String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]))

export const reduced = () => typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches
