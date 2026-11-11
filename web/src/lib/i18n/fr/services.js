// Services login (lib/services.js): the methods, the presets, how a login
// went and what's wrong with the fields.
export default {
  'services.method.auto': 'Automatique — SASL, sinon NickServ',
  'services.method.sasl': 'SASL seulement',
  'services.method.nickserv': 'IDENTIFY à NickServ',
  'services.method.command': 'Une commande personnalisée',
  'services.method.none': 'Ne pas s’identifier',

  'services.preset.auto': 'Automatique (SASL, puis NickServ)',
  'services.preset.sasl': 'SASL seulement (Libera.Chat, ergo…)',
  'services.preset.atheme': 'NickServ — Atheme ou Anope',
  'services.preset.dalnet': 'DALnet',
  'services.preset.undernet': 'Undernet (X)',
  'services.preset.quakenet': 'QuakeNet (Q, avec le module q)',
  'services.preset.custom': 'Personnalisé',

  'services.auth.pending': 'En attente des services…',
  'services.auth.identified': 'Identifié',
  'services.auth.failed': 'Identification refusée',
  'services.auth.unconfirmed': 'Les services n’ont pas répondu',

  'services.badServicesNick': 'Un seul mot, comme NickServ ou NickServ@services.dal.net.',
  'services.badAuthCommand': 'Saisissez la commande qui vous identifie.',
  'services.badAuthTimeout': 'Secondes, 0–300 (0 = 15).',
}
