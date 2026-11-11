// Login nos serviços (lib/services.js): os métodos, as predefinições, como
// foi um login e o que há de errado nos campos.
export default {
  'services.method.auto': 'Automático — SASL, senão NickServ',
  'services.method.sasl': 'Só SASL',
  'services.method.nickserv': 'IDENTIFY ao NickServ',
  'services.method.command': 'Um comando meu',
  'services.method.none': 'Não fazer login',

  'services.preset.auto': 'Automático (SASL, depois NickServ)',
  'services.preset.sasl': 'Só SASL (Libera.Chat, ergo…)',
  'services.preset.atheme': 'NickServ — Atheme ou Anope',
  'services.preset.dalnet': 'DALnet',
  'services.preset.undernet': 'Undernet (X)',
  'services.preset.quakenet': 'QuakeNet (Q, com o módulo q)',
  'services.preset.custom': 'Personalizado',

  'services.auth.pending': 'Esperando pelos serviços…',
  'services.auth.identified': 'Logado',
  'services.auth.failed': 'Login recusado',
  'services.auth.unconfirmed': 'Os serviços não responderam',

  'services.badServicesNick': 'Uma palavra, como NickServ ou NickServ@services.dal.net.',
  'services.badAuthCommand': 'Digite o comando que faz seu login.',
  'services.badAuthTimeout': 'Segundos, 0–300 (0 = 15).',
}
