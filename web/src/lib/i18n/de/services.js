// Services login (lib/services.js): the methods, the presets, how a login
// went and what's wrong with the fields.
export default {
  'services.method.auto': 'Automatisch — SASL, sonst NickServ',
  'services.method.sasl': 'Nur SASL',
  'services.method.nickserv': 'IDENTIFY bei NickServ',
  'services.method.command': 'Ein eigener Befehl',
  'services.method.none': 'Nicht anmelden',

  'services.preset.auto': 'Automatisch (SASL, dann NickServ)',
  'services.preset.sasl': 'Nur SASL (Libera.Chat, ergo…)',
  'services.preset.atheme': 'NickServ — Atheme oder Anope',
  'services.preset.dalnet': 'DALnet',
  'services.preset.undernet': 'Undernet (X)',
  'services.preset.quakenet': 'QuakeNet (Q, mit dem q-Modul)',
  'services.preset.custom': 'Benutzerdefiniert',

  'services.auth.pending': 'Warte auf die Services…',
  'services.auth.identified': 'Angemeldet',
  'services.auth.failed': 'Anmeldung abgelehnt',
  'services.auth.unconfirmed': 'Die Services haben nicht geantwortet',

  'services.badServicesNick': 'Ein Wort, z. B. NickServ oder NickServ@services.dal.net.',
  'services.badAuthCommand': 'Gib den Befehl ein, mit dem du dich anmeldest.',
  'services.badAuthTimeout': 'Sekunden, 0–300 (0 = 15).',
}
