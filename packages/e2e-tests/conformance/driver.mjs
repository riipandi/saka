// The conformance driver: the browser leg the suite cannot perform by
// itself. The suite drives its server-to-server protocol calls and then
// hands the runner a browser URL — the authorization request, the
// end-session request — that a user agent must walk to the end.
//
// The browser is a real Chromium, pointed at the issuer the way the SPA
// will one day point itself: the interaction document the authorization
// endpoint answers is read, the consent decision is posted to the
// interaction endpoint — the same POST the SPA's consent page makes — and
// the redirect that answers is followed. The credential that
// authenticates the account rides the request the debug flow drives:
// the deployment's bearer. There is no test-only path here — the pages
// the browser walks are the protocol's own.
//
// The hosts resolve through Chromium's --host-resolver-rules, so no
// /etc/hosts edit is asked of the operator, and the two prebuilt
// certificates (the suite's and the local issuer's) are accepted — a
// rehearsal inspects behavior, not the wire's paperwork.

import { chromium } from '@playwright/test'
import https from 'node:https'
import { URL } from 'node:url'

// postDecision performs the consent decision's POST the way the SPA's
// consent page will: the interaction endpoint answers the redirect the
// browser follows next. The request rides node:https with a lookup that
// pins both rehearsal hostnames to the loopback — the runner process has
// no Chromium resolver of its own — and reads the 303's Location off the
// answer instead of following it.
function postDecision(url, bearer, body) {
  const target = new URL(url)
  return new Promise((resolve, reject) => {
    const req = https.request(
      target,
      {
        method: 'POST',
        rejectUnauthorized: false,
        lookup: (hostname, options, callback) => {
          // The rehearsal's two hostnames both live on the loopback;
          // the all:true form the socket asks for answers the array
          // shape.
          if (options?.all) {
            callback(null, [{ address: '127.0.0.1', family: 4 }])
            return
          }
          callback(null, '127.0.0.1', 4)
        },
        headers: {
          Authorization: `Bearer ${bearer}`,
          'Content-Type': 'application/json',
          'Content-Length': Buffer.byteLength(body)
        }
      },
      (res) => {
        const location = res.headers.location
        res.resume()
        res.on('end', () => resolve({ status: res.statusCode, location }))
      }
    )
    req.on('error', reject)
    req.write(body)
    req.end()
  })
}

export async function openDriver({ bearer, issuerOrigin }) {
  const browser = await chromium.launch({
    args: [
      '--host-resolver-rules=MAP localhost.emobix.co.uk 127.0.0.1, MAP saka.localhost.test 127.0.0.1'
    ]
  })
  const context = await browser.newContext({
    ignoreHTTPSErrors: true,
    viewport: { width: 1280, height: 800 },
    extraHTTPHeaders: { Authorization: `Bearer ${bearer}` }
  })
  const page = await context.newPage()
  return {
    browser,
    // drive walks one browser URL the suite asked for. When the URL
    // leaves the issuer — the suite's callback — the leg is done.
    drive: (url) => driveBrowserURL(page, url, { issuerOrigin, bearer }),
    // driveWithFinalUrl answers the page's final URL — the callback the
    // leg landed on.
    driveWithFinalUrl: async (url) => {
      await driveBrowserURL(page, url, { issuerOrigin, bearer })
      return page.url()
    },
    // clearCookies drops the OP's browser-session marker — the suite asks
    // for it when a test must meet a user agent that has never
    // authenticated here.
    clearCookies: () => context.clearCookies(),
    // screenshot captures the page as it stands — the evidence some
    // modules ask the operator to upload before they continue.
    screenshot: () =>
      page
        .screenshot({ type: 'jpeg', quality: 50 })
        .then((buffer) => `data:image/jpeg;base64,${buffer.toString('base64')}`),
    close: () => browser.close()
  }
}

// driveBrowserURL walks one authorization or end-session request: the
// browser lands either on the interaction document or on a redirect whose
// destination is already the suite. An interaction document is completed —
// the consent decision posted to the interaction endpoint — and the
// redirect that answers is the browser's next hop.
async function driveBrowserURL(page, url, { issuerOrigin, bearer }) {
  await page.goto(url, { waitUntil: 'domcontentloaded' })

  if (!page.url().startsWith(issuerOrigin)) {
    return 'left-the-issuer'
  }

  const document = await page.evaluate(() => {
    try {
      return JSON.parse(document.body.innerText)
    } catch {
      return null
    }
  })
  if (!document?.interaction_id) {
    // The failure mode the report names: what the browser actually
    // landed on — the scaffold redirect page when the credential did
    // not reach the authorize endpoint, a protocol error document
    // otherwise.
    const body = await page.evaluate(() => document.body.innerText).catch(() => '')
    console.log(
      `  drive: url=${page.url().slice(0, 110)} body=${body.slice(0, 160).replace(/\n/g, ' ')}`
    )
    return 'no-interaction-document'
  }

  // The consent decision: every scope the document asked for — the same
  // answer the SPA's allow button would post. The redirect that answers
  // is the browser's next hop.
  const answer = await postDecision(
    `${issuerOrigin}/oidc/authorize/${document.interaction_id}`,
    bearer,
    JSON.stringify({ scopes: document.requested_scopes ?? [] })
  )
  if (!answer.location) {
    return `decision-answered-${answer.status}`
  }

  await page.goto(answer.location, { waitUntil: 'domcontentloaded' })
  return page.url().startsWith(issuerOrigin) ? 'still-on-issuer' : 'redirected'
}
