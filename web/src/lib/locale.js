// The web app's languages, and which one to use (no Svelte here, so plain
// Node tests can use it; i18n.svelte.js does the rest).

// Languages offered: code, and the name in that language.
export const LANGUAGES = [
  { code: 'en', name: 'English' },
  { code: 'it', name: 'Italiano' },
  { code: 'es', name: 'Español' },
  { code: 'tr', name: 'Türkçe' },
  { code: 'de', name: 'Deutsch' },
  { code: 'id', name: 'Bahasa Indonesia' },
  { code: 'ja', name: '日本語' },
]

// pick is the language to use: the account's choice, else the first of the
// browser's languages the app has, else English. codes are the languages
// on offer (the built-in ones, plus any language packs').
export function pick(chosen, browser = globalThis.navigator?.languages || [], codes = LANGUAGES.map((l) => l.code)) {
  const have = (c) => codes.find((x) => x.toLowerCase() === c.toLowerCase())
  if (chosen && have(chosen)) return have(chosen)
  for (const b of browser) {
    const c = have(String(b)) || have(String(b).split('-')[0])
    if (c) return c
  }
  return 'en'
}
