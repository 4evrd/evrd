// Autentificarea la servicii (lib/services.js): metodele, presetările, cum a
// decurs autentificarea și ce e greșit în câmpuri. DALnet și Undernet (X) rămân
// ca în engleză: sunt doar nume.
export default {
  'services.method.auto': 'Automat — SASL, altfel NickServ',
  'services.method.sasl': 'Doar SASL',
  'services.method.nickserv': 'IDENTIFY la NickServ',
  'services.method.command': 'O comandă proprie',
  'services.method.none': 'Fără autentificare',

  'services.preset.auto': 'Automat (SASL, apoi NickServ)',
  'services.preset.sasl': 'Doar SASL (Libera.Chat, ergo…)',
  'services.preset.atheme': 'NickServ — Atheme sau Anope',
  'services.preset.dalnet': 'DALnet',
  'services.preset.undernet': 'Undernet (X)',
  'services.preset.quakenet': 'QuakeNet (Q, cu modulul q)',
  'services.preset.custom': 'Personalizat',

  'services.auth.pending': 'Se așteaptă serviciile…',
  'services.auth.identified': 'Autentificat',
  'services.auth.failed': 'Autentificare refuzată',
  'services.auth.unconfirmed': 'Serviciile n-au răspuns',

  'services.badServicesNick': 'Un singur cuvânt, ca NickServ sau NickServ@services.dal.net.',
  'services.badAuthCommand': 'Introdu comanda cu care te autentifici.',
  'services.badAuthTimeout': 'Secunde, 0–300 (0 = 15).',
}
