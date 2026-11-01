// Ore, durate, saluturi și nume de dispozitive (lib/format.js).
export default {
  'format.never': 'niciodată',
  'format.justNow': 'chiar acum',
  'format.minutesAgo': { one: 'acum {count} minut', few: 'acum {count} minute', other: 'acum {count} de minute' },
  'format.hoursAgo': { one: 'acum {count} oră', few: 'acum {count} ore', other: 'acum {count} de ore' },
  'format.yesterday': 'ieri',
  'format.daysAgo': { one: 'acum {count} zi', few: 'acum {count} zile', other: 'acum {count} de zile' },
  'format.weeksAgo': { one: 'acum {count} săptămână', few: 'acum {count} săptămâni', other: 'acum {count} de săptămâni' },
  // durate scurte: „acum”, „45s”, „12m”, „1h 2m”, „11z 4h”
  'format.now': 'acum',
  'format.seconds': '{s}s',
  'format.minutes': '{m}m',
  'format.hours': '{h}h',
  'format.hoursMinutes': '{h}h {m}m',
  'format.days': '{d}z',
  'format.daysHours': '{d}z {h}h',
  // o oră pe un grafic care încă nu s-a încheiat: „08:00–acum”
  'format.untilNow': '{from}–acum',
  'format.greetingLate': 'Încă treaz',
  'format.greetingMorning': 'Bună dimineața',
  'format.greetingAfternoon': 'Bună ziua',
  'format.greetingEvening': 'Bună seara',
  'format.unknownDevice': 'Dispozitiv necunoscut',
  'format.browser': 'Browser',
  // „Chrome pe macOS”
  'format.browserOn': '{browser} pe {os}',
}
