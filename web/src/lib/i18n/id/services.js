// Services login (lib/services.js): the methods, the presets, how a login
// went and what's wrong with the fields.
export default {
  'services.method.auto': 'Otomatis — SASL, kalau gagal NickServ',
  'services.method.sasl': 'Hanya SASL',
  'services.method.nickserv': 'IDENTIFY ke NickServ',
  'services.method.command': 'Perintah buatan sendiri',
  'services.method.none': 'Jangan login',

  'services.preset.auto': 'Otomatis (SASL, lalu NickServ)',
  'services.preset.sasl': 'Hanya SASL (Libera.Chat, ergo…)',
  'services.preset.atheme': 'NickServ — Atheme atau Anope',
  'services.preset.dalnet': 'DALnet',
  'services.preset.undernet': 'Undernet (X)',
  'services.preset.quakenet': 'QuakeNet (Q, dengan modul q)',
  'services.preset.custom': 'Kustom',

  'services.auth.pending': 'Menunggu services…',
  'services.auth.identified': 'Sudah login',
  'services.auth.failed': 'Login ditolak',
  'services.auth.unconfirmed': 'Services tidak menjawab',

  'services.badServicesNick': 'Satu kata, seperti NickServ atau NickServ@services.dal.net.',
  'services.badAuthCommand': 'Masukkan perintah untuk login.',
  'services.badAuthTimeout': 'Detik, 0–300 (0 = 15).',
}
