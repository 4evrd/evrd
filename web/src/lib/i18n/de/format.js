// Times, durations, greetings and device names (lib/format.js).
export default {
  'format.never': 'nie',
  'format.justNow': 'gerade eben',
  'format.minutesAgo': { one: 'vor {count} Minute', other: 'vor {count} Minuten' },
  'format.hoursAgo': { one: 'vor {count} Stunde', other: 'vor {count} Stunden' },
  'format.yesterday': 'gestern',
  'format.daysAgo': { one: 'vor {count} Tag', other: 'vor {count} Tagen' },
  'format.weeksAgo': { one: 'vor {count} Woche', other: 'vor {count} Wochen' },
  // short durations: "jetzt", "45 s", "12 min", "1 h 2 min", "11 T 4 h"
  'format.now': 'jetzt',
  'format.seconds': '{s} s',
  'format.minutes': '{m} min',
  'format.hours': '{h} h',
  'format.hoursMinutes': '{h} h {m} min',
  'format.days': '{d} T',
  'format.daysHours': '{d} T {h} h',
  // an hour on a chart that is still going: "08:00–jetzt"
  'format.untilNow': '{from}–jetzt',
  'format.greetingLate': 'Noch wach',
  'format.greetingMorning': 'Guten Morgen',
  'format.greetingAfternoon': 'Guten Tag',
  'format.greetingEvening': 'Guten Abend',
  'format.unknownDevice': 'Unbekanntes Gerät',
  'format.browser': 'Browser',
  // "Chrome unter macOS"
  'format.browserOn': '{browser} unter {os}',
}
