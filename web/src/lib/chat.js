// The web chat's slash commands and event wording. parse() turns a line typed
// in a buffer into what to send; describe() words a channel event the same way
// the bouncer replays it to IRC clients. The words are in i18n/<lang>/chat.js.

import { t } from './i18n.svelte.js'

export const isChan = (t) => /^[#&+!]/.test(t || '')

// [syntax, message key of its description]
export const COMMANDS = [
  ['/join #channel [key]', 'chat.cmd.join'],
  ['/part [#channel] [reason]', 'chat.cmd.part'],
  ['/msg nick text', 'chat.cmd.msg'],
  ['/query nick', 'chat.cmd.query'],
  ['/me text', 'chat.cmd.me'],
  ['/notice target text', 'chat.cmd.notice'],
  ['/nick newnick', 'chat.cmd.nick'],
  ['/topic [text]', 'chat.cmd.topic'],
  ['/mode [target] modes', 'chat.cmd.mode'],
  ['/kick nick [reason]', 'chat.cmd.kick'],
  ['/op /deop /voice /devoice nick…', 'chat.cmd.op'],
  ['/ban /unban nick|mask', 'chat.cmd.ban'],
  ['/invite nick [#channel]', 'chat.cmd.invite'],
  ['/whois nick', 'chat.cmd.whois'],
  ['/away [message]', 'chat.cmd.away'],
  ['/back', 'chat.cmd.back'],
  ['/ctcp nick command', 'chat.cmd.ctcp'],
  ['/ns /cs text', 'chat.cmd.services'],
  ['/close', 'chat.cmd.close'],
  ['/quote line', 'chat.cmd.quote'],
  ['/status command', 'chat.cmd.status'],
  ['/help', 'chat.cmd.help'],
  ['/anything else', 'chat.cmd.other'],
]

// modeFor are the commands that set a mode on nicks: [sign and letter].
const modeFor = { op: '+o', deop: '-o', voice: '+v', devoice: '-v' }

// services are the shortcuts for messaging a network's services.
const services = { ns: 'NickServ', cs: 'ChanServ', ms: 'MemoServ', hs: 'HostServ', bs: 'BotServ', os: 'OperServ' }

// parse returns one of:
//   { say: text }                       a message to the current buffer
//   { send: { target, text, kind } }    a message/action/notice elsewhere
//   { raw: line }                       a raw IRC line
//   { open: nick, send? }               open a PM buffer (and maybe send)
//   { status: command }                 a *status command
//   { close: true } | { help: true }
//   { error: '…' }
// Any other /command goes to the network as typed, as from an IRC client
// (its replies come back as reply frames). aliases are the network's own
// commands (aliasesOf): they win over the built-in ones, and evrd's alias
// module expands them.
export function parse(input, target, aliases = {}) {
  const line = input.trim()
  if (!line) return null
  const plain = !line.startsWith('/') || line.startsWith('//')
  if (plain && /^\*status$/i.test(target || '')) return { status: line.replace(/^\/\//, '/'), open: '*status' }
  if (plain) return { say: line.startsWith('//') ? line.slice(1) : line }
  const [, cmd, rest = ''] = line.match(/^\/(\S+)\s*(.*)$/)
  const args = rest.split(/\s+/).filter(Boolean)
  const here = isChan(target) ? target : ''
  const need = (ok, usage) => (ok ? null : { error: t('chat.usage', { usage }) })
  const name = cmd.toLowerCase()
  const asTyped = { raw: rest ? `${cmd.toUpperCase()} ${rest}` : cmd.toUpperCase() }
  if (Object.hasOwn(aliases, name) && !['help', 'commands', 'close', 'status', 'znc'].includes(name)) return asTyped

  if (modeFor[name]) {
    if (!here) return { error: t('chat.inChannel', { command: '/' + name }) }
    if (!args.length) return { error: t('chat.usage', { usage: `/${name} nick…` }) }
    return { raw: `MODE ${here} ${modeFor[name][0]}${modeFor[name][1].repeat(args.length)} ${args.join(' ')}` }
  }
  if (services[name]) return need(rest, `/${name} text`) || { send: { target: services[name], text: rest } }

  switch (name) {
    case 'join': case 'j':
      return need(args[0], '/join #channel') || { raw: `JOIN ${isChan(args[0]) ? args[0] : '#' + args[0]}${args[1] ? ' ' + args[1] : ''}` }
    case 'part': case 'leave': {
      const ch = isChan(args[0]) ? args.shift() : here
      if (!ch) return { error: t('chat.whichChannel') }
      return { raw: `PART ${ch}${args.length ? ' :' + args.join(' ') : ''}` }
    }
    case 'msg': case 'privmsg': {
      const m = rest.match(/^(\S+)\s+(.+)$/)
      if (!m) return { error: t('chat.usage', { usage: '/msg nick text' }) }
      if (/^\*status$/i.test(m[1])) return { status: m[2], open: '*status' }
      return isChan(m[1]) ? { send: { target: m[1], text: m[2] } } : { open: m[1], send: { target: m[1], text: m[2] } }
    }
    case 'status': case 'znc':
      return { status: rest || 'help', open: '*status' }
    case 'query': case 'q': {
      const m = rest.match(/^(\S+)\s*(.*)$/)
      if (!m) return { error: t('chat.usage', { usage: '/query nick' }) }
      if (/^\*status$/i.test(m[1])) return { status: m[2] || 'help', open: '*status' }
      return { open: m[1], send: m[2] ? { target: m[1], text: m[2] } : undefined }
    }
    case 'me':
      return need(rest && target, '/me text') || { send: { target, text: rest, kind: 'action' } }
    case 'notice': {
      const m = rest.match(/^(\S+)\s+(.+)$/)
      return m ? { send: { target: m[1], text: m[2], kind: 'notice' } } : { error: t('chat.usage', { usage: '/notice target text' }) }
    }
    case 'nick':
      return need(args.length === 1, '/nick newnick') || { raw: `NICK ${args[0]}` }
    case 'topic':
      if (!here) return { error: t('chat.inChannel', { command: '/topic' }) }
      return { raw: rest ? `TOPIC ${here} :${rest}` : `TOPIC ${here}` }
    case 'mode':
      if (!args.length) return { error: t('chat.usage', { usage: '/mode [target] modes' }) }
      return { raw: isChan(args[0]) || !here ? `MODE ${rest}` : `MODE ${here} ${rest}` }
    case 'kick': {
      if (!here) return { error: t('chat.inChannel', { command: '/kick' }) }
      const [nick, ...why] = args
      return need(nick, '/kick nick [reason]') || { raw: `KICK ${here} ${nick}${why.length ? ' :' + why.join(' ') : ''}` }
    }
    case 'ban': case 'unban': {
      if (!here) return { error: t('chat.inChannel', { command: '/' + name }) }
      if (!args.length) return { error: t('chat.usage', { usage: `/${name} nick|mask` }) }
      const masks = args.map((a) => (/[!@*]/.test(a) ? a : `${a}!*@*`))
      return { raw: `MODE ${here} ${name === 'ban' ? '+' : '-'}${'b'.repeat(masks.length)} ${masks.join(' ')}` }
    }
    case 'invite': {
      const ch = isChan(args[1]) ? args[1] : here
      if (!args[0] || !ch) return { error: t('chat.usage', { usage: '/invite nick [#channel]' }) }
      return { raw: `INVITE ${args[0]} ${ch}` }
    }
    case 'whois': case 'wi':
      return need(args[0], '/whois nick') || { raw: `WHOIS ${args.join(' ')}` }
    case 'away':
      return { raw: rest ? `AWAY :${rest}` : 'AWAY' }
    case 'back':
      return { raw: 'AWAY' }
    case 'ctcp': {
      const [nick, what, ...more] = args
      if (!nick || !what) return { error: t('chat.usage', { usage: '/ctcp nick command' }) }
      return { raw: `PRIVMSG ${nick} :\x01${[what.toUpperCase(), ...more].join(' ')}\x01` }
    }
    case 'quit': case 'disconnect':
      return { error: t('chat.noQuit') }
    case 'close':
      return { close: true }
    case 'quote': case 'raw':
      return need(rest, '/quote line') || { raw: rest }
    case 'help': case 'commands':
      return { help: true }
  }
  return asTyped
}

// aliasesOf is a network's aliases, name → commands, when its alias module
// is on (one "name = commands" per line, the same as the bouncer reads them).
export function aliasesOf(net) {
  const out = {}
  const m = net?.modules?.alias
  if (!m?.enabled) return out
  for (const l of (m.settings?.aliases || '').split('\n')) {
    const at = l.indexOf('=')
    if (at < 0) continue
    const name = l.slice(0, at).trim().replace(/^\//, '').toLowerCase()
    if (name && !/\s/.test(name)) out[name] = l.slice(at + 1).trim()
  }
  return out
}

// tableCells splits a Markdown table row ("| a | b|c | d") into its cells,
// or returns null for a line that isn't one. *status sends tables this way,
// in IRC's monospace formatting (\x11). Only a "|" with spaces around it
// breaks a column, so "<channel|nick>" stays one cell; with columns given,
// what's past the last column stays in it. The closing "|" is optional, and
// "\|" is how older evrd wrote a "|" in a cell.
export function tableCells(text, columns = Infinity) {
  text = (text || '').replaceAll('\x11', '').replace(/\s\|\s*$/, '')
  if (text[0] !== '|') return null
  const cells = []
  let cur = ''
  for (let i = 1; i < text.length; i++) {
    const sep = text[i] === '|' && /\s/.test(text[i - 1]) && (i + 1 === text.length || /\s/.test(text[i + 1]))
    if (text[i] === '\\' && text[i + 1] === '|') { cur += '|'; i++ } else if (sep && cells.length < columns - 1) { cells.push(cur.trim()); cur = '' } else cur += text[i]
  }
  if (cur.trim()) cells.push(cur.trim())
  return cells
}
export const isTableRule = (cells) => cells.length > 0 && cells.every((c) => /^:?-{3,}:?$/.test(c))

export function describe(m) {
  const v = { nick: m.sender, other: m.extra, text: m.text }
  // part, quit and kick have a form with the reason
  const why = (k) => t(m.text ? `${k}Reason` : k, v)
  switch (m.kind) {
    case 'join': return t('chat.ev.join', v)
    case 'part': return why('chat.ev.part')
    case 'quit': return why('chat.ev.quit')
    case 'kick': return why('chat.ev.kick')
    case 'nick': return t('chat.ev.nick', v)
    case 'topic': return t('chat.ev.topic', v)
    case 'mode': return t('chat.ev.mode', v)
  }
  return m.text
}

export const isEvent = (kind) => !['message', 'notice', 'action', 'system', 'local', ''].includes(kind || '')
