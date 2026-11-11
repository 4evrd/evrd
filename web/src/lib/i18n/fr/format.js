// Times, durations, greetings and device names (lib/format.js).
export default {
  'format.never': 'jamais',
  'format.justNow': 'à l’instant',
  'format.minutesAgo': { one: 'il y a {count} minute', other: 'il y a {count} minutes' },
  'format.hoursAgo': { one: 'il y a {count} heure', other: 'il y a {count} heures' },
  'format.yesterday': 'hier',
  'format.daysAgo': { one: 'il y a {count} jour', other: 'il y a {count} jours' },
  'format.weeksAgo': { one: 'il y a {count} semaine', other: 'il y a {count} semaines' },
  // short durations: "now", "45s", "12m", "1h 2m", "11d 4h"
  'format.now': 'maintenant',
  'format.seconds': '{s} s',
  'format.minutes': '{m} min',
  'format.hours': '{h} h',
  'format.hoursMinutes': '{h} h {m} min',
  'format.days': '{d} j',
  'format.daysHours': '{d} j {h} h',
  // an hour on a chart that is still going: "08:00–now"
  'format.untilNow': '{from}–maintenant',
  'format.greetingLate': 'Encore debout',
  'format.greetingMorning': 'Bonjour',
  'format.greetingAfternoon': 'Bon après-midi',
  'format.greetingEvening': 'Bonsoir',
  'format.unknownDevice': 'Appareil inconnu',
  'format.browser': 'Navigateur',
  // "Chrome on macOS"
  'format.browserOn': '{browser} sur {os}',
}
