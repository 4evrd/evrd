// Inline form errors (lib/validate.js).
export default {
  'validate.required': 'この項目は必須です。',
  'validate.name': '見分けがつくように名前を付けてください。',
  'validate.nameLong': '32 文字以内にしてください。',
  'validate.netnameChars': 'スペース、/、: は使えません。この名前は IRC のサーバーパスワードに含まれます。',
  'validate.hostEmpty': 'サーバーのアドレスを入力してください（例: irc.libera.chat）。',
  'validate.host': 'ホスト名ではないようです。英字、数字、ドット、ハイフンを使ってください（例: irc.oftc.net）。',
  'validate.port': 'ポートは 1〜65535 の整数です。TLS なら 6697、TLS なしなら 6667 です。',
  'validate.nickEmpty': 'ニックネームを入力してください。',
  'validate.nick': 'ニックネームは英字で始まり、英字、数字、[ ] \\ ` _ ^ { | } - が使えます。',
  'validate.channels': 'チャンネルはカンマで区切り、それぞれ # で始めてください（例: #evrd, #go-nuts）。',
  'validate.username': '英小文字で始まる 2〜32 文字で、英小文字、数字、-、_ が使えます。',
  'validate.count': '0 以上の整数を入力してください。空欄にするとデフォルト値になります。',
  'validate.password': '10 文字以上にしてください。',
  'validate.email': 'メールアドレスではないようです。',
  'validate.match': 'パスワードが一致しません。',
}
