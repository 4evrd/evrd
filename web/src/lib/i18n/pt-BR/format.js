// Times, durations, greetings and device names (lib/format.js).
export default {
  'format.never': 'nunca',
  'format.justNow': 'agora mesmo',
  'format.minutesAgo': { one: 'há {count} minuto', other: 'há {count} minutos' },
  'format.hoursAgo': { one: 'há {count} hora', other: 'há {count} horas' },
  'format.yesterday': 'ontem',
  'format.daysAgo': { one: 'há {count} dia', other: 'há {count} dias' },
  'format.weeksAgo': { one: 'há {count} semana', other: 'há {count} semanas' },
  // short durations: "now", "45s", "12m", "1h 2m", "11d 4h"
  'format.now': 'agora',
  'format.seconds': '{s}s',
  'format.minutes': '{m}min',
  'format.hours': '{h}h',
  'format.hoursMinutes': '{h}h {m}min',
  'format.days': '{d}d',
  'format.daysHours': '{d}d {h}h',
  // an hour on a chart that is still going: "08:00–now"
  'format.untilNow': '{from}–agora',
  'format.greetingLate': 'Acordado até tarde',
  'format.greetingMorning': 'Bom dia',
  'format.greetingAfternoon': 'Boa tarde',
  'format.greetingEvening': 'Boa noite',
  'format.unknownDevice': 'Dispositivo desconhecido',
  'format.browser': 'Navegador',
  // "Chrome on macOS"
  'format.browserOn': '{browser} no {os}',
}
