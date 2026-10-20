import net from 'node:net'

// A raw IRC client for tests: attaches to evrd's IRC listener the way irssi or
// HexChat would (PASS user/network:password), records every line and lets a
// test wait for one. caps, if any, are negotiated the way a modern client
// does: CAP LS 302, PASS/NICK/USER, then CAP REQ once the LS answer is in
// (so the network's capabilities can be had), and CAP END once that's
// answered. With sasl it signs in with SASL PLAIN instead of PASS; before are
// lines sent before NICK and USER (BOUNCER BIND…).
export class IRC {
  static connect({ user, network, password, nick, caps, sasl, before = [], version = 'e2e-client 1.0', host = process.env.E2E_IRC_HOST || 'localhost', port = +(process.env.E2E_IRC_PORT || 6667) }) {
    const c = new IRC()
    c.version = version
    const login = `${user}${network ? '/' + network : ''}`
    const want = [...(caps || []), ...(sasl ? ['sasl'] : [])]
    const pending = new Set(want)
    // what comes after each step of the negotiation
    c.negotiate = (line) => {
      if (/^:\S+ CAP \S+ LS :/.test(line) && pending.size === want.length) c.send(`CAP REQ :${want.join(' ')}`)
      const answer = line.match(/^:\S+ CAP \S+ (ACK|NAK) :(.*)$/)
      if (answer && pending.size) {
        for (const cap of answer[2].split(' ')) pending.delete(cap)
        if (pending.size) return
        if (sasl && c.lines.some((l) => / CAP \S+ ACK :(.* )?sasl( |$)/.test(l))) c.send('AUTHENTICATE PLAIN')
        else c.send('CAP END')
      }
      if (line === 'AUTHENTICATE +') c.send('AUTHENTICATE ' + Buffer.from(`\0${login}\0${password}`).toString('base64'))
      if (/^:\S+ 90[34] /.test(line)) c.send('CAP END')
    }
    return new Promise((resolve, reject) => {
      c.sock = net.connect(port, host, () => {
        if (want.length) c.send('CAP LS 302')
        if (!sasl) c.send(`PASS ${login}:${password}`)
        for (const l of before) c.send(l)
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
          c.negotiate(line)
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
