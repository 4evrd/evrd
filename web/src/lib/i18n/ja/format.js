// Times, durations, greetings and device names (lib/format.js).
export default {
  'format.never': 'なし',
  'format.justNow': 'たった今',
  'format.minutesAgo': { other: '{count} 分前' },
  'format.hoursAgo': { other: '{count} 時間前' },
  'format.yesterday': '昨日',
  'format.daysAgo': { other: '{count} 日前' },
  'format.weeksAgo': { other: '{count} 週間前' },
  // short durations: "now", "45s", "12m", "1h 2m", "11d 4h"
  'format.now': '現在',
  'format.seconds': '{s}秒',
  'format.minutes': '{m}分',
  'format.hours': '{h}時間',
  'format.hoursMinutes': '{h}時間{m}分',
  'format.days': '{d}日',
  'format.daysHours': '{d}日{h}時間',
  // an hour on a chart that is still going: "08:00–now"
  'format.untilNow': '{from}–現在',
  'format.greetingLate': '夜更かしですね',
  'format.greetingMorning': 'おはようございます',
  'format.greetingAfternoon': 'こんにちは',
  'format.greetingEvening': 'こんばんは',
  'format.unknownDevice': '不明なデバイス',
  'format.browser': 'ブラウザー',
  // "Chrome on macOS"
  'format.browserOn': '{browser}（{os}）',
}
