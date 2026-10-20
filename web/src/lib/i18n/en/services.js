// Services login (lib/services.js): the methods, the presets, how a login
// went and what's wrong with the fields.
export default {
  'services.method.auto': 'Automatic — SASL, else NickServ',
  'services.method.sasl': 'SASL only',
  'services.method.nickserv': 'IDENTIFY to NickServ',
  'services.method.command': 'A command of my own',
  'services.method.none': 'Don’t log in',

  'services.preset.auto': 'Automatic (SASL, then NickServ)',
  'services.preset.sasl': 'SASL only (Libera.Chat, ergo…)',
  'services.preset.atheme': 'NickServ — Atheme or Anope',
  'services.preset.dalnet': 'DALnet',
  'services.preset.undernet': 'Undernet (X)',
  'services.preset.quakenet': 'QuakeNet (Q, with the q module)',
  'services.preset.custom': 'Custom',

  'services.auth.pending': 'Waiting for services…',
  'services.auth.identified': 'Logged in',
  'services.auth.failed': 'Login refused',
  'services.auth.unconfirmed': 'Services didn’t answer',

  'services.badServicesNick': 'One word, like NickServ or NickServ@services.dal.net.',
  'services.badAuthCommand': 'Enter the command that logs you in.',
  'services.badAuthTimeout': 'Seconds, 0–300 (0 = 15).',
}
