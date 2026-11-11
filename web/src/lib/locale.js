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
  { code: 'ro', name: 'Română' },
  { code: 'ja', name: '日本語' },
  { code: 'fr', name: 'Français' },
  { code: 'ms', name: 'Bahasa Melayu' },
  { code: 'pt-BR', name: 'Português (Brasil)' },
]

// pick is the language to use: the account's choice, else the first of the
// browser's languages the app has (pt-PT or pt gets pt-BR when that's the
// only Portuguese), else English. codes are the languages on offer (the
// built-in ones, plus any language packs').
export function pick(chosen, browser = globalThis.navigator?.languages || [], codes = LANGUAGES.map((l) => l.code)) {
  const have = (c) => codes.find((x) => x.toLowerCase() === c.toLowerCase())
  const base = (c) => c.split('-')[0].toLowerCase()
  if (chosen && have(chosen)) return have(chosen)
  for (const b of browser) {
    const c = have(String(b)) || have(base(String(b))) || codes.find((x) => base(x) === base(String(b)))
    if (c) return c
  }
  return 'en'
}
