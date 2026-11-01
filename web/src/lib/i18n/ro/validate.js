// Erori de formular afișate lângă câmp (lib/validate.js).
export default {
  'validate.required': 'Acest câmp este obligatoriu.',
  'validate.name': 'Dă-i un nume ca să-l poți deosebi.',
  'validate.nameLong': 'Folosește cel mult 32 de caractere.',
  'validate.netnameChars': 'Fără spații, / sau : — numele intră în parola serverului tău IRC.',
  'validate.hostEmpty': 'Introdu adresa serverului, de ex. irc.libera.chat.',
  'validate.host': 'Nu pare un nume de host — folosește litere, cifre, puncte și cratime, de ex. irc.oftc.net.',
  'validate.port': 'Porturile sunt numere întregi de la 1 la 65535 — 6697 pentru TLS, 6667 fără.',
  'validate.nickEmpty': 'Alege un nick.',
  'validate.nick': 'Nickurile încep cu o literă și folosesc litere, cifre sau [ ] \\ ` _ ^ { | } -.',
  'validate.channels': 'Separă canalele cu virgule; fiecare începe cu # (de ex. #evrd, #go-nuts).',
  'validate.username': 'Folosește 2–32 de litere mici, cifre, - sau _, începând cu o literă.',
  'validate.count': 'Introdu un număr întreg, 0 sau mai mare — ori lasă gol pentru valoarea implicită.',
  'validate.password': 'Folosește cel puțin 10 caractere.',
  'validate.email': 'Nu pare o adresă de email.',
  'validate.match': 'Parolele nu coincid.',
}
