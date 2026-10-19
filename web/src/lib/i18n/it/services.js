// Accesso ai servizi (lib/services.js). DALnet e Undernet (X) restano come
// in inglese: sono solo nomi.
export default {
  'services.method.auto': 'Automatico: SASL, altrimenti NickServ',
  'services.method.sasl': 'Solo SASL',
  'services.method.nickserv': 'IDENTIFY a NickServ',
  'services.method.command': 'Un comando mio',
  'services.method.none': 'Non accedere',

  'services.preset.auto': 'Automatico (SASL, poi NickServ)',
  'services.preset.sasl': 'Solo SASL (Libera.Chat, ergo…)',
  'services.preset.atheme': 'NickServ: Atheme o Anope',
  'services.preset.quakenet': 'QuakeNet (Q, con il modulo q)',
  'services.preset.custom': 'Personalizzato',

  'services.auth.pending': 'In attesa dei servizi…',
  'services.auth.identified': 'Accesso effettuato',
  'services.auth.failed': 'Accesso rifiutato',
  'services.auth.unconfirmed': 'I servizi non hanno risposto',

  'services.badServicesNick': 'Una parola sola, come NickServ o NickServ@services.dal.net.',
  'services.badAuthCommand': 'Inserisci il comando che ti fa accedere.',
  'services.badAuthTimeout': 'Secondi, 0–300 (0 = 15).',
}
