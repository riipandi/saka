package webauthn

import "net/http"

// handleProbe serves the development probe page: the test instrument a real
// browser walks the ceremonies through. It is a page, not a surface — the
// wiring mounts it in the development mode alone, and the token it acts on
// is the caller's own.
func handleProbe(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(probePage))
}

// probePage is the development probe. Vanilla JS, no build step: the
// ceremony encoding rides @simplewebauthn/browser from unpkg — the UMD
// bundle exposes SimpleWebAuthnBrowser.startRegistration/startAuthentication,
// which take the server's options JSON verbatim and answer the response JSON
// the verify procedures carry. The page walks the ceremonies with the
// browser's own navigator.credentials and renders what the server answered;
// the RPC surface sits behind the same host the page came from.
const probePage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Tango WebAuthn probe</title>
<style>
  body { font: 14px/1.5 ui-monospace, monospace; margin: 2rem; background: #111; color: #ddd; }
  input, button { font: inherit; padding: .4rem .7rem; margin: .2rem 0; }
  button { cursor: pointer; }
  field { display: block; margin-bottom: .8rem; }
  #log { white-space: pre-wrap; border-top: 1px solid #444; margin-top: 1rem; padding-top: .5rem; }
  .ok { color: #7c7; } .err { color: #d77; }
</style>
</head>
<body>
<h2>Tango WebAuthn probe</h2>
<p>Development instrument. The token is the access token a sign-in answers with.</p>
<field><input id="token" size="80" placeholder="access token"></field>
<field><input id="name" size="30" value="Probe key"></field>
<button id="enroll">1. Enroll passkey</button>
<button id="signin">2. Sign in passwordless</button>
<button id="stepup">3. Step-up (passkey)</button>
<div id="log"></div>
<script src="https://unpkg.com/@simplewebauthn/browser@14.0.0/dist/bundle/index.umd.min.js"></script>
<script>
const { startRegistration, startAuthentication } = SimpleWebAuthnBrowser;

const log = (kind, message) => {
  const line = document.createElement('div');
  line.className = kind;
  line.textContent = message;
  document.getElementById('log').appendChild(line);
};

const rpc = async (procedure, body, withToken) => {
  const headers = { 'Content-Type': 'application/json', 'Connect-Protocol-Version': '1' };
  const token = document.getElementById('token').value.trim();
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
};

document.getElementById('enroll').onclick = async () => {
  try {
    const { options, sessionId } = await ceremony('tango.authn.v1.WebAuthnService/BeginRegistration', true);
    const credential = await startRegistration({ optionsJSON: options });
    const answer = await rpc('tango.authn.v1.WebAuthnService/VerifyRegistration', {
      session_id: sessionId,
      name: document.getElementById('name').value,
      credential,
    }, true);
    log('ok', 'enrolled: ' + answer.credential.id + ' — ' + answer.credential.name);
  } catch (error) { log('err', 'enroll: ' + error.message); }
};

document.getElementById('signin').onclick = async () => {
  try {
    const { options, sessionId } = await ceremony('tango.authn.v1.WebAuthnService/BeginLogin', false);
    const assertion = await startAuthentication({ optionsJSON: options });
    const answer = await rpc('tango.authn.v1.WebAuthnService/VerifyLogin', {
      session_id: sessionId,
      credential: assertion,
    }, false);
    log('ok', 'signed in as ' + answer.user.username + ' — token in the field above');
    document.getElementById('token').value = answer.access_token;
  } catch (error) { log('err', 'sign-in: ' + error.message); }
};

document.getElementById('stepup').onclick = async () => {
  try {
    const { options, sessionId } = await ceremony('tango.authn.v1.WebAuthnService/BeginLogin', false);
    const assertion = await startAuthentication({ optionsJSON: options });
    const answer = await rpc('tango.authn.v1.WebAuthnService/Reauthenticate', {
      passkey: { session_id: sessionId, credential: assertion },
    }, true);
    log('ok', 'step-up proof expires at ' + answer.expires_at + ' — spend it on a guarded call');
  } catch (error) { log('err', 'step-up: ' + error.message); }
};
</script>
</body>
</html>`
