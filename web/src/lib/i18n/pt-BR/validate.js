// Erros de formulário em linha (lib/validate.js).
export default {
  'validate.required': 'Este campo é obrigatório.',
  'validate.name': 'Dê um nome para distinguir dos outros.',
  'validate.nameLong': 'Use no máximo 32 caracteres.',
  'validate.netnameChars': 'Sem espaços, / ou : — o nome vai na senha do servidor de IRC.',
  'validate.hostEmpty': 'Digite o endereço do servidor, por exemplo irc.libera.chat.',
  'validate.host': 'Isso não parece um nome de host — use letras, dígitos, pontos e hifens, por exemplo irc.oftc.net.',
  'validate.port': 'Portas são números inteiros de 1 a 65535 — 6697 para TLS, 6667 sem.',
  'validate.nickEmpty': 'Escolha um nick.',
  'validate.nick': 'Nicks começam com uma letra e usam letras, dígitos ou [ ] \\ ` _ ^ { | } -.',
  'validate.channels': 'Separe os canais com vírgulas; cada um começa com # (por exemplo #evrd, #go-nuts).',
  'validate.username': 'Use de 2 a 32 letras minúsculas, dígitos, - ou _, começando com uma letra.',
  'validate.count': 'Digite um número inteiro, 0 ou mais — ou deixe vazio para usar o padrão.',
  'validate.password': 'Use pelo menos 10 caracteres.',
  'validate.email': 'Isso não parece um endereço de e-mail.',
  'validate.match': 'As senhas não coincidem.',
}
