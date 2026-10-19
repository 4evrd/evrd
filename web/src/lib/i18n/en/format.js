// Times, durations, greetings and device names (lib/format.js).
export default {
  'format.never': 'never',
  'format.justNow': 'just now',
  'format.minutesAgo': { one: '{count} minute ago', other: '{count} minutes ago' },
  'format.hoursAgo': { one: '{count} hour ago', other: '{count} hours ago' },
  'format.yesterday': 'yesterday',
  'format.daysAgo': { one: '{count} day ago', other: '{count} days ago' },
  'format.weeksAgo': { one: '{count} week ago', other: '{count} weeks ago' },
  // short durations: "now", "45s", "12m", "1h 2m", "11d 4h"
  'format.now': 'now',
  'format.seconds': '{s}s',
  'format.minutes': '{m}m',
  'format.hours': '{h}h',
  'format.hoursMinutes': '{h}h {m}m',
  'format.days': '{d}d',
  'format.daysHours': '{d}d {h}h',
  // an hour on a chart that is still going: "08:00–now"
  'format.untilNow': '{from}–now',
  'format.greetingLate': 'Up late',
  'format.greetingMorning': 'Morning',
  'format.greetingAfternoon': 'Afternoon',
  'format.greetingEvening': 'Evening',
  'format.unknownDevice': 'Unknown device',
  'format.browser': 'Browser',
  // "Chrome on macOS"
  'format.browserOn': '{browser} on {os}',
}
