//go:build debug

// The WebAuthn probe pages' body, debug build only: the transport's devtool
// surface serves them at /debug/passkey/*, and a release build carries none
// of this (its counterpart refuses the path with the 404 envelope).

package web

// WebauthnProbePages maps the devtool's passkey routes to their bodies. The
// pages are the simulation of the SPA surfaces the frontend work will
// replace: per-flow, vanilla JS, no build step, and the ceremony encoding
// rides @simplewebauthn/browser's UMD bundle from unpkg —
// SimpleWebAuthnBrowser.startRegistration/startAuthentication take the
// server's options JSON verbatim and answer the response JSON the verify
// procedures carry. The token each page asks for is the access token a
// sign-in answers with; the page's origin is the RP origin the server
// expects.
var WebauthnProbePages = map[string]string{
	"/debug/passkey":        webauthnProbeIndexPage,
	"/debug/passkey/enroll": webauthnProbeEnrollPage,
	"/debug/passkey/signin": webauthnProbeSigninPage,
	"/debug/passkey/stepup": webauthnProbeStepupPage,
}

// webauthnProbeStyle is the shared look of the probe pages.
const webauthnProbeStyle = `
  body { font: 14px/1.5 ui-monospace, monospace; margin: 2rem; background: #111; color: #ddd; }
  input, button { font: inherit; padding: .4rem .7rem; margin: .2rem 0; }
  button { cursor: pointer; }
  field { display: block; margin-bottom: .8rem; }
  #log { white-space: pre-wrap; border-top: 1px solid #444; margin-top: 1rem; padding-top: .5rem; }
  .ok { color: #7c7; } .err { color: #d77; }`

// webauthnProbeScript is the shared page script: the log helper, the RPC
// caller, and the ceremony opener the buttons ride.
const webauthnProbeScript = `
const log = (kind, message) => {
  const line = document.createElement('div');
  line.className = kind;
  line.textContent = message;
  document.getElementById('log').appendChild(line);
};

const rpc = async (procedure, body, withToken) => {
  const headers = { 'Content-Type': 'application/json', 'Connect-Protocol-Version': '1' };
  // A page without the token field is the passwordless one: no header,
  // because the credential is the whole request.
  const token = document.getElementById('token')?.value.trim() ?? '';
  if (withToken && token) headers['Authorization'] = 'Bearer ' + token;
  const response = await fetch('/rpc/' + procedure, {
    method: 'POST', headers, body: JSON.stringify(body),
  });
  const answer = await response.json().catch(() => ({}));
  if (response.status !== 200) throw new Error(response.status + ' ' + JSON.stringify(answer));
  return answer;
};

const ceremony = async (procedure, withToken) => {
  const answer = await rpc(procedure, {}, withToken);
  return { options: JSON.parse(answer.options).publicKey, sessionId: answer.session_id };
};`

// webauthnProbeIndexPage names the pages and repeats the runbook.
const webauthnProbeIndexPage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Saka passkey probe</title>
<style>` + webauthnProbeStyle + `</style>
</head>
<body>
<h2>Saka passkey probe</h2>
<p>Development instrument — the simulation of the SPA surfaces the frontend work will replace.
The token each page asks for is the access token a sign-in answers with:
<code>scripts/curl-rpc.sh http://localhost:3080/rpc/saka.authn.v1.AuthService/SignIn '{"identity":"admin","password":"…"}'</code></p>
<ul>
  <li><a href="/debug/passkey/enroll">/debug/passkey/enroll</a> — enroll a passkey on the authenticated session</li>
  <li><a href="/debug/passkey/signin">/debug/passkey/signin</a> — sign in passwordless</li>
  <li><a href="/debug/passkey/stepup">/debug/passkey/stepup</a> — mint the step-up proof</li>
</ul>
</body>
</html>`

// webauthnProbeEnrollPage is the enrollment simulation.
const webauthnProbeEnrollPage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Saka passkey probe — enroll</title>
<style>` + webauthnProbeStyle + `</style>
</head>
<body>
<h2>Enroll</h2>
<field><input id="token" size="80" placeholder="access token"></field>
<field><input id="name" size="30" value="Probe key" data-testid="credential-name"></field>
<button id="enroll" data-testid="enroll">Enroll passkey</button>
<div id="log" data-testid="log"></div>
<script src="https://unpkg.com/@simplewebauthn/browser@14.0.0/dist/bundle/index.umd.min.js"></script>
<script>
const { startRegistration } = SimpleWebAuthnBrowser;` + webauthnProbeScript + `

document.getElementById('enroll').onclick = async () => {
  try {
    const { options, sessionId } = await ceremony('saka.authn.v1.WebAuthnService/BeginRegistration', true);
    const credential = await startRegistration({ optionsJSON: options });
    const answer = await rpc('saka.authn.v1.WebAuthnService/VerifyRegistration', {
      session_id: sessionId,
      name: document.getElementById('name').value,
      // The contract carries the browser's JSON verbatim — a string field.
      credential: JSON.stringify(credential),
    }, true);
    log('ok', 'enrolled: ' + answer.credential.id + ' — ' + answer.credential.name);
  } catch (error) { log('err', 'enroll: ' + error.message); }
};
</script>
</body>
</html>`

// webauthnProbeSigninPage is the passwordless sign-in simulation.
const webauthnProbeSigninPage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Saka passkey probe — sign in</title>
<style>` + webauthnProbeStyle + `</style>
</head>
<body>
<h2>Sign in</h2>
<button id="signin" data-testid="signin">Sign in with a passkey</button>
<div id="log" data-testid="log"></div>
<script src="https://unpkg.com/@simplewebauthn/browser@14.0.0/dist/bundle/index.umd.min.js"></script>
<script>
const { startAuthentication } = SimpleWebAuthnBrowser;` + webauthnProbeScript + `

document.getElementById('signin').onclick = async () => {
  try {
    const { options, sessionId } = await ceremony('saka.authn.v1.WebAuthnService/BeginLogin', false);
    const assertion = await startAuthentication({ optionsJSON: options });
    const answer = await rpc('saka.authn.v1.WebAuthnService/VerifyLogin', {
      session_id: sessionId,
      // The contract carries the browser's JSON verbatim — a string field.
      credential: JSON.stringify(assertion),
    }, false);
    log('ok', 'signed in as ' + answer.user.username);
    log('ok', 'session: ' + answer.session_id);
    log('ok', 'access token: ' + answer.access_token);
  } catch (error) { log('err', 'sign-in: ' + error.message); }
};
</script>
</body>
</html>`

// webauthnProbeStepupPage is the step-up simulation.
const webauthnProbeStepupPage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Saka passkey probe — step-up</title>
<style>` + webauthnProbeStyle + `</style>
</head>
<body>
<h2>Step-up</h2>
<field><input id="token" size="80" placeholder="access token"></field>
<button id="stepup" data-testid="stepup">Mint the reauthentication proof</button>
<div id="log" data-testid="log"></div>
<script src="https://unpkg.com/@simplewebauthn/browser@14.0.0/dist/bundle/index.umd.min.js"></script>
<script>
const { startAuthentication } = SimpleWebAuthnBrowser;` + webauthnProbeScript + `

document.getElementById('stepup').onclick = async () => {
  try {
    const { options, sessionId } = await ceremony('saka.authn.v1.WebAuthnService/BeginLogin', true);
    const assertion = await startAuthentication({ optionsJSON: options });
    const answer = await rpc('saka.authn.v1.WebAuthnService/Reauthenticate', {
      passkey: {
        session_id: sessionId,
        // The contract carries the browser's JSON verbatim — a string field.
        credential: JSON.stringify(assertion),
      },
    }, true);
    log('ok', 'proof: ' + answer.token);
    log('ok', 'expires at ' + answer.expires_at + ' — spend it on a guarded call');
  } catch (error) { log('err', 'step-up: ' + error.message); }
};
</script>
</body>
</html>`
