import { writable } from 'svelte/store'

// connectionBloc — BLoC for the evrd WebSocket session.
// State flows out via stores; intent flows in via methods.
//
// Scaffold: it tracks status and exposes connect/disconnect. The IRCv3 framing
// over the socket (CAP, AUTHENTICATE, attach to a buffer) lands on top of this.
export function createConnectionBloc() {
  const status = writable('idle') // idle | connecting | open | closed | error
  const log = writable([])

  let ws = null

  function push(line) {
    log.update((l) => [...l, line])
  }

  function connect(url) {
    if (ws) return
    status.set('connecting')
    push(`* connecting to ${url}`)

    ws = new WebSocket(url)
    ws.onopen = () => {
      status.set('open')
      push('* socket open')
    }
    ws.onmessage = (ev) => push(`< ${ev.data}`)
    ws.onerror = () => status.set('error')
    ws.onclose = () => {
      status.set('closed')
      push('* socket closed')
      ws = null
    }
  }

  function send(line) {
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(line)
      push(`> ${line}`)
    }
  }

  function disconnect() {
    if (ws) ws.close()
  }

  return { status, log, connect, send, disconnect }
}
