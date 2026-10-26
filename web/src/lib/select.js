// Keyboard logic for the custom dropdown (components/Select.svelte): which
// option a key moves to. Options are { value, label, disabled? }.

// step moves from index i by delta (±1) to the next enabled option,
// stopping at the ends; with nothing active it starts from the matching end.
export function step(options, i, delta) {
  let j = i < 0 ? (delta > 0 ? -1 : options.length) : i
  for (j += delta; j >= 0 && j < options.length; j += delta) if (!options[j].disabled) return j
  return i
}

// edge is the first (or last) enabled option.
export const edge = (options, last) => step(options, last ? options.length : -1, last ? -1 : 1)

// typeahead finds the next enabled option whose label starts with query,
// searching after i for a single character (so repeating it cycles) and
// from i for a longer query; -1 when none matches.
export function typeahead(options, query, i) {
  const q = query.toLowerCase()
  if (!q) return -1
  const n = options.length
  const start = q.length === 1 ? i + 1 : Math.max(i, 0)
  for (let k = 0; k < n; k++) {
    const j = (((start + k) % n) + n) % n
    const o = options[j]
    if (!o.disabled && String(o.label).toLowerCase().startsWith(q)) return j
  }
  return -1
}

// indexOf is the option holding value (-1 when none does).
export const indexOf = (options, value) => options.findIndex((o) => o.value === value)

// filter keeps the options whose label (or extra search text, o.find)
// contains query, ignoring case; all of them for an empty query.
export function filter(options, query) {
  const q = query.trim().toLowerCase()
  if (!q) return options
  return options.filter((o) => `${o.label} ${o.find || ''}`.toLowerCase().includes(q))
}
