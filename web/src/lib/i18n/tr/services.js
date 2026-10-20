// Servislere giriş (lib/services.js). DALnet ve Undernet (X) İngilizcedeki
// gibi kalır: yalnızca ad.
export default {
  'services.method.auto': 'Otomatik: SASL, yoksa NickServ',
  'services.method.sasl': 'Yalnızca SASL',
  'services.method.nickserv': 'NickServ’e IDENTIFY',
  'services.method.command': 'Kendi komutum',
  'services.method.none': 'Giriş yapma',

  'services.preset.auto': 'Otomatik (SASL, sonra NickServ)',
  'services.preset.sasl': 'Yalnızca SASL (Libera.Chat, ergo…)',
  'services.preset.atheme': 'NickServ: Atheme ya da Anope',
  'services.preset.quakenet': 'QuakeNet (Q, q modülüyle)',
  'services.preset.custom': 'Özel',

  'services.auth.pending': 'Servisler bekleniyor…',
  'services.auth.identified': 'Giriş yapıldı',
  'services.auth.failed': 'Giriş reddedildi',
  'services.auth.unconfirmed': 'Servisler yanıt vermedi',

  'services.badServicesNick': 'Tek sözcük, örneğin NickServ ya da NickServ@services.dal.net.',
  'services.badAuthCommand': 'Seni giriş yaptıran komutu yaz.',
  'services.badAuthTimeout': 'Saniye, 0–300 (0 = 15).',
}
