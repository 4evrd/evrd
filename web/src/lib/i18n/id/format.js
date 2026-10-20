// Times, durations, greetings and device names (lib/format.js).
export default {
  'format.never': 'tidak pernah',
  'format.justNow': 'baru saja',
  'format.minutesAgo': { other: '{count} menit lalu' },
  'format.hoursAgo': { other: '{count} jam lalu' },
  'format.yesterday': 'kemarin',
  'format.daysAgo': { other: '{count} hari lalu' },
  'format.weeksAgo': { other: '{count} minggu lalu' },
  // short durations: "now", "45s", "12m", "1h 2m", "11d 4h"
  'format.now': 'sekarang',
  'format.seconds': '{s}d',
  'format.minutes': '{m}m',
  'format.hours': '{h}j',
  'format.hoursMinutes': '{h}j {m}m',
  'format.days': '{d}h',
  'format.daysHours': '{d}h {h}j',
  // an hour on a chart that is still going: "08:00–now"
  'format.untilNow': '{from}–sekarang',
  'format.greetingLate': 'Masih begadang',
  'format.greetingMorning': 'Selamat pagi',
  'format.greetingAfternoon': 'Selamat siang',
  'format.greetingEvening': 'Selamat malam',
  'format.unknownDevice': 'Perangkat tak dikenal',
  'format.browser': 'Browser',
  // "Chrome on macOS"
  'format.browserOn': '{browser} di {os}',
}
