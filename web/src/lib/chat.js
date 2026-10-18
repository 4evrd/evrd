// The web chat's slash commands and event wording. parse() turns a line typed
// in a buffer into what to send; describe() words a channel event the same way
// the bouncer replays it to IRC clients.

export const isChan = (t) => /^[#&+!]/.test(t || '')

export const COMMANDS = [
  ['/join #channel [key]', 'Join a channel (it’s saved to this network)'],
  ['/part [#channel] [reason]', 'Leave a channel (default: this one)'],
  ['/msg nick text', 'Message someone — opens a private buffer'],
  ['/query nick', 'Open a private buffer'],
  ['/me text', 'An action: * you text'],
  ['/notice target text', 'Send a notice'],
  ['/nick newnick', 'Change your nick'],
  ['/topic [text]', 'Set this channel’s topic'],
  ['/mode [target] modes', 'Set modes (default: this channel)'],
  ['/kick nick [reason]', 'Kick someone from this channel'],
  ['/close', 'Close this private buffer'],
  ['/quote line', 'Send a raw IRC line (replies show in IRC clients)'],
  ['/status command', 'Bouncer commands, same as /msg *status (try /status help)'],
  ['/help', 'This list'],
]

// parse returns one of:
//   { say: text }                       a message to the current buffer
//   { send: { target, text, kind } }    a message/action/notice elsewhere
//   { raw: line }                       a raw IRC line
//   { open: nick, send? }               open a PM buffer (and maybe send)
//   { status: command }                 a *status command
//   { close: true } | { help: true }
//   { error: '…' }
export function parse(input, target) {
  const line = input.trim()
  if (!line) return null
  const plain = !line.startsWith('/') || line.startsWith('//')
  if (plain && /^\*status$/i.test(target || '')) return { status: line.replace(/^\/\//, '/'), open: '*status' }
  if (plain) return { say: line.startsWith('//') ? line.slice(1) : line }
  const [, cmd, rest = ''] = line.match(/^\/(\S+)\s*(.*)$/)
  const args = rest.split(/\s+/).filter(Boolean)
  const here = isChan(target) ? target : ''
  const need = (ok, usage) => (ok ? null : { error: `Usage: ${usage}` })

  switch (cmd.toLowerCase()) {
    case 'join': case 'j':
      return need(args[0], '/join #channel') || { raw: `JOIN ${isChan(args[0]) ? args[0] : '#' + args[0]}${args[1] ? ' ' + args[1] : ''}` }
    case 'part': case 'leave': {
      const ch = isChan(args[0]) ? args.shift() : here
      if (!ch) return { error: 'Which channel? /part #channel' }
      return { raw: `PART ${ch}${args.length ? ' :' + args.join(' ') : ''}` }
    }
    case 'msg': case 'privmsg': {
      const m = rest.match(/^(\S+)\s+(.+)$/)
      if (!m) return { error: 'Usage: /msg nick text' }
      if (/^\*status$/i.test(m[1])) return { status: m[2], open: '*status' }
      return isChan(m[1]) ? { send: { target: m[1], text: m[2] } } : { open: m[1], send: { target: m[1], text: m[2] } }
    }
    case 'status':
      return { status: rest || 'help', open: '*status' }
    case 'query': case 'q': {
      const m = rest.match(/^(\S+)\s*(.*)$/)
      if (!m) return { error: 'Usage: /query nick' }
      if (/^\*status$/i.test(m[1])) return { status: m[2] || 'help', open: '*status' }
      return { open: m[1], send: m[2] ? { target: m[1], text: m[2] } : undefined }
    }
    case 'me':
      return need(rest && target, '/me text') || { send: { target, text: rest, kind: 'action' } }
    case 'notice': {
      const m = rest.match(/^(\S+)\s+(.+)$/)
      return m ? { send: { target: m[1], text: m[2], kind: 'notice' } } : { error: 'Usage: /notice target text' }
    }
    case 'nick':
      return need(args.length === 1, '/nick newnick') || { raw: `NICK ${args[0]}` }
    case 'topic':
      if (!here) return { error: 'Use /topic in a channel.' }
      return { raw: rest ? `TOPIC ${here} :${rest}` : `TOPIC ${here}` }
    case 'mode':
      if (!args.length) return { error: 'Usage: /mode [target] modes' }
      return { raw: isChan(args[0]) || !here ? `MODE ${rest}` : `MODE ${here} ${rest}` }
    case 'kick': {
      if (!here) return { error: 'Use /kick in a channel.' }
      const [nick, ...why] = args
      return need(nick, '/kick nick [reason]') || { raw: `KICK ${here} ${nick}${why.length ? ' :' + why.join(' ') : ''}` }
    }
    case 'close':
      return { close: true }
    case 'quote': case 'raw':
      return need(rest, '/quote line') || { raw: rest }
    case 'help': case 'commands':
      return { help: true }
  }
  return { error: `/${cmd} isn’t supported here — try /help, or use an IRC client.` }
}

export function describe(m) {
  const why = (s) => (s ? ` (${s})` : '')
  switch (m.kind) {
    case 'join': return `${m.sender} joined`
    case 'part': return `${m.sender} left${why(m.text)}`
    case 'quit': return `${m.sender} quit${why(m.text)}`
    case 'kick': return `${m.sender} kicked ${m.extra}${why(m.text)}`
    case 'nick': return `${m.sender} is now known as ${m.extra}`
    case 'topic': return `${m.sender} changed the topic to: ${m.text}`
    case 'mode': return `${m.sender} set mode ${m.text}`
  }
  return m.text
}

export const isEvent = (kind) => !['message', 'notice', 'action', 'system', 'local', ''].includes(kind || '')
