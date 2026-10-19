// Inicio de sesión en los servicios (lib/services.js). DALnet y Undernet (X)
// quedan como en inglés: son solo nombres.
export default {
  'services.method.auto': 'Automático: SASL, si no NickServ',
  'services.method.sasl': 'Solo SASL',
  'services.method.nickserv': 'IDENTIFY a NickServ',
  'services.method.command': 'Un comando propio',
  'services.method.none': 'No iniciar sesión',

  'services.preset.auto': 'Automático (SASL, luego NickServ)',
  'services.preset.sasl': 'Solo SASL (Libera.Chat, ergo…)',
  'services.preset.atheme': 'NickServ: Atheme o Anope',
  'services.preset.quakenet': 'QuakeNet (Q, con el módulo q)',
  'services.preset.custom': 'Personalizado',

  'services.auth.pending': 'Esperando a los servicios…',
  'services.auth.identified': 'Sesión iniciada',
  'services.auth.failed': 'Inicio de sesión rechazado',
  'services.auth.unconfirmed': 'Los servicios no respondieron',

  'services.badServicesNick': 'Una sola palabra, como NickServ o NickServ@services.dal.net.',
  'services.badAuthCommand': 'Escribe el comando con el que inicias sesión.',
  'services.badAuthTimeout': 'Segundos, 0–300 (0 = 15).',
}
