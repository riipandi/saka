// The code an email step waits for, read out of Mailpit's API — the
// ladder's inbox. A clear before each send keeps a previous run's
// email from answering a step with a stale code.

export async function mailpitClear() {
  try {
    await fetch('http://localhost:8025/api/v1/messages', { method: 'DELETE' })
  } catch {
    /* mailpit down is a step's own failure to report */
  }
}

async function mailpitCode(recipient) {
  const r = await fetch('http://localhost:8025/api/v1/messages?limit=10')
  const { messages = [] } = await r.json()
  for (const message of messages) {
    const addresses = (message.To ?? []).map((a) => a.Address)
    if (!addresses.includes(recipient)) continue
    const full = await (await fetch(`http://localhost:8025/api/v1/message/${message.ID}`)).json()
    const match = /<span[^>]*letter-spacing[^>]*>\s*([A-Za-z0-9]+)\s*<\/span>/s.exec(
      full.HTML ?? ''
    )
    if (match) return match[1]
  }
  return null
}

export async function mailpitWait(recipient, tries = 30) {
  for (let i = 0; i < tries; i++) {
    const code = await mailpitCode(recipient)
    if (code) return code
    await new Promise((r) => setTimeout(r, 2000))
  }
  return null
}
