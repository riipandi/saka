import FingerprintJS, { type UnknownComponents } from '@fingerprintjs/fingerprintjs'

/**
 * The device headers the server's audit records and session rows consume:
 * the browser fingerprint under `X-Device-Fingerprint` and the user agent
 * under `User-Agent`. The transport middleware reads both headers and every
 * consumer below it — the audit recorder, the session rows, the known-device
 * notice — sees what landed here.
 *
 * The agent loads once per page; every caller awaits the same promise. A
 * browser that refuses the fingerprinting answers no headers at all — the
 * server records a fingerprint-less client, which is the state it already
 * treats as the default.
 */
let device: Promise<Record<string, string>> | null = null

export function deviceHeaders(): Promise<Record<string, string>> {
  device ??= FingerprintJS.load({ monitoring: false })
    .then((agent) => agent.get())
    .then((result) => {
      // The components map is typed loosely upstream — a component is either
      // its value or an error marker — and the user agent is the one field
      // this module needs from it.
      const ua = (result.components as UnknownComponents).userAgent
      return {
        'x-device-fingerprint': result.visitorId,
        'user-agent': String(ua && 'value' in ua ? ua.value : navigator.userAgent)
      }
    })
    .catch(() => ({}))
  return device
}
