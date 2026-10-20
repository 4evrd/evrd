// Services login (lib/services.js): the methods, the presets, how a login
// went and what's wrong with the fields.
export default {
  'services.method.auto': '自動 — SASL、使えなければ NickServ',
  'services.method.sasl': 'SASL のみ',
  'services.method.nickserv': 'NickServ に IDENTIFY',
  'services.method.command': '独自のコマンド',
  'services.method.none': 'ログインしない',

  'services.preset.auto': '自動（SASL、次に NickServ）',
  'services.preset.sasl': 'SASL のみ（Libera.Chat、ergo など）',
  'services.preset.atheme': 'NickServ — Atheme または Anope',
  'services.preset.dalnet': 'DALnet',
  'services.preset.undernet': 'Undernet（X）',
  'services.preset.quakenet': 'QuakeNet（Q、q モジュール使用）',
  'services.preset.custom': 'カスタム',

  'services.auth.pending': 'サービスの応答を待っています…',
  'services.auth.identified': 'ログイン済み',
  'services.auth.failed': 'ログインが拒否されました',
  'services.auth.unconfirmed': 'サービスから応答がありません',

  'services.badServicesNick': '1 語で入力してください（例: NickServ、NickServ@services.dal.net）。',
  'services.badAuthCommand': 'ログインに使うコマンドを入力してください。',
  'services.badAuthTimeout': '秒数（0〜300、0 = 15）。',
}
