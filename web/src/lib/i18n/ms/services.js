// Services login (lib/services.js): the methods, the presets, how a login
// went and what's wrong with the fields.
export default {
  'services.method.auto': 'Automatik — SASL, jika tidak NickServ',
  'services.method.sasl': 'SASL sahaja',
  'services.method.nickserv': 'IDENTIFY kepada NickServ',
  'services.method.command': 'Arahan saya sendiri',
  'services.method.none': 'Jangan log masuk',

  'services.preset.auto': 'Automatik (SASL, kemudian NickServ)',
  'services.preset.sasl': 'SASL sahaja (Libera.Chat, ergo…)',
  'services.preset.atheme': 'NickServ — Atheme atau Anope',
  'services.preset.dalnet': 'DALnet',
  'services.preset.undernet': 'Undernet (X)',
  'services.preset.quakenet': 'QuakeNet (Q, dengan modul q)',
  'services.preset.custom': 'Tersuai',

  'services.auth.pending': 'Menunggu services…',
  'services.auth.identified': 'Telah log masuk',
  'services.auth.failed': 'Log masuk ditolak',
  'services.auth.unconfirmed': 'Services tidak menjawab',

  'services.badServicesNick': 'Satu perkataan, seperti NickServ atau NickServ@services.dal.net.',
  'services.badAuthCommand': 'Masukkan arahan yang melog masuk anda.',
  'services.badAuthTimeout': 'Saat, 0–300 (0 = 15).',
}
