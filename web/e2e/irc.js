import net from 'node:net'

// A raw IRC client for tests: attaches to evrd's IRC listener the way irssi or
// HexChat would (PASS user/network:password), records every line and lets a
// test wait for one. caps, if any, are requested (CAP LS 302) before signing in.
export class IRC {
  static connect({ user, network, password, nick, caps, version = 'e2e-client 1.0', host = process.env.E2E_IRC_HOST || 'localhost', port = +(process.env.E2E_IRC_PORT || 6667) }) {
    const c = new IRC()
    c.version = version
    return new Promise((resolve, reject) => {
      c.sock = net.connect(port, host, () => {
        if (caps?.length) {
          c.send('CAP LS 302')
          c.send(`CAP REQ :${caps.join(' ')}`)
          c.send('CAP END')
        }
        c.send(`PASS ${user}${network ? '/' + network : ''}:${password}`)
        c.send(`NICK ${nick || user}`)
        c.send(`USER ${user} 0 * :${user}`)
        resolve(c)
      })
      c.sock.on('error', reject)
      let buf = ''
      c.sock.on('data', (d) => {
        buf += d.toString('utf8')
        let i
        while ((i = buf.indexOf('\r\n')) >= 0) {
          const line = buf.slice(0, i)
          buf = buf.slice(i + 2)
          c.lines.push(line)
          if (line.startsWith('PING ')) c.send('PONG ' + line.slice(5))
          // answer evrd's CTCP VERSION so it can name this client
          const v = line.match(/^:(\S+?)!\S+ PRIVMSG \S+ :\x01VERSION\x01$/)
          if (v && c.version) c.send(`NOTICE ${v[1]} :\x01VERSION ${c.version}\x01`)
          for (const w of [...c.waiters]) if (w.re.test(line)) { c.waiters.delete(w); w.resolve(line) }
        }
      })
    })
  }

  lines = []
  waiters = new Set()

  send(line) { this.sock.write(line + '\r\n') }

  // waitFor resolves with the first line (already received or later) matching re.
  waitFor(re, timeout = 10000) {
    const hit = this.lines.find((l) => re.test(l))
    if (hit) return Promise.resolve(hit)
    return new Promise((resolve, reject) => {
      const w = { re, resolve }
      this.waiters.add(w)
      setTimeout(() => { if (this.waiters.delete(w)) reject(new Error(`no line matching ${re} within ${timeout}ms; got:\n${this.lines.slice(-15).join('\n')}`)) }, timeout)
    })
  }

  // next waits only for lines that arrive after this call.
  next(re, timeout = 10000) {
    const seen = this.lines.length
    return new Promise((resolve, reject) => {
      const w = { re: { test: (l) => re.test(l) && this.lines.indexOf(l, seen) >= seen }, resolve }
      this.waiters.add(w)
      setTimeout(() => { if (this.waiters.delete(w)) reject(new Error(`no new line matching ${re} within ${timeout}ms`)) }, timeout)
    })
  }

  closed() { return new Promise((r) => (this.sock.destroyed ? r() : this.sock.once('close', r))) }
  close() { try { this.send('QUIT :bye') } catch { /* gone */ } this.sock.destroy() }
}
