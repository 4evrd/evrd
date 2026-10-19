// The web app's languages, and which one to use (no Svelte here, so plain
// Node tests can use it; i18n.svelte.js does the rest).

// Languages offered: code, and the name in that language.
export const LANGUAGES = [
  { code: 'en', name: 'English' },
  { code: 'it', name: 'Italiano' },
  { code: 'es', name: 'Español' },
  { code: 'tr', name: 'Türkçe' },
]

// pick is the language to use: the account's choice, else the first of the
// browser's languages the app has, else English.
export function pick(chosen, browser = globalThis.navigator?.languages || []) {
  const have = (c) => LANGUAGES.some((l) => l.code === c)
  if (chosen && have(chosen)) return chosen
  for (const b of browser) {
    const c = String(b).toLowerCase().split('-')[0]
    if (have(c)) return c
  }
  return 'en'
}
