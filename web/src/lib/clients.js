// How an attached client is named in lists. A client signed in with the SSO
// password or by link is a device per address ("SSO password from
// 203.0.113.5"); where the address shows on its own anyway, it isn't said
// twice.

// deviceLabel is c.device without " from <c.ip>".
export function deviceLabel(c) {
  const from = c.ip ? ` from ${c.ip}` : ''
  return from && c.device?.endsWith(from) ? c.device.slice(0, -from.length) : c.device || ''
}
