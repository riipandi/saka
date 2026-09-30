//go:build debug

// The devtool surface of a debug build: the samber/do web UI under /debug/do,
// and the TypeID codecs a developer turns a log line's identifier into the
// UUID a query wants. The routes sit outside the throttled and bearer-guarded
// groups — they are a developer's window into the process, not a client
// surface — and no authentication guards them, because a release build
// refuses the same paths with a 404 envelope (devtool_release.go).

package transport

import (
	"encoding/json/v2"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/samber/do/v2"
	dohttp "github.com/samber/do/v2/http"
	"go.jetify.com/typeid"

	"github.com/riipandi/tango/pkg/responder"
)

func mountDevtool(r chi.Router, injector do.Injector) {
	r.Get(devtoolUIPath, func(w http.ResponseWriter, r *http.Request) {
		html, err := dohttp.IndexHTML(devtoolUIPath)
		devtoolPage(w, html, err)
	})
	r.Get(devtoolUIPath+"/scope", func(w http.ResponseWriter, r *http.Request) {
		scopeID := r.URL.Query().Get("scope_id")
		if scopeID == "" {
			http.Redirect(w, r, devtoolUIPath+"/scope?scope_id="+injector.ID(), http.StatusFound)
			return
		}
		html, err := dohttp.ScopeTreeHTML(devtoolUIPath, injector, scopeID)
		devtoolPage(w, html, err)
	})
	r.Get(devtoolUIPath+"/service", func(w http.ResponseWriter, r *http.Request) {
		scopeID := r.URL.Query().Get("scope_id")
		serviceName := r.URL.Query().Get("service_name")
		if scopeID == "" || serviceName == "" {
			html, err := dohttp.ServiceListHTML(devtoolUIPath, injector)
			devtoolPage(w, html, err)
			return
		}
		html, err := dohttp.ServiceHTML(devtoolUIPath, injector, scopeID, serviceName)
		devtoolPage(w, html, err)
	})
	r.Post("/debug/encode-id", encodeID)
	r.Post("/debug/decode-id", decodeID)
	r.Get(webauthnProbePath, webauthnProbe)
}

// webauthnProbe serves the passkey ceremony probe. It drives the ceremonies
// through @simplewebauthn/browser's UMD bundle from unpkg —
// SimpleWebAuthnBrowser.startRegistration/startAuthentication take the
// server's options JSON verbatim and answer the response JSON the verify
// procedures carry — so a real browser walks the same verification the soft
// authenticator exercises in Go (the passkey plan's Phase B instrument).
func webauthnProbe(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(webauthnProbePage))
}

// webauthnProbePage is the probe's body. The token is the access token a
// sign-in answers with; the page's origin is the RP origin the server
// expects.
const webauthnProbePage = `<!doctype html>
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

// devtoolID is the codec's answer on both directions: the identifier in its
// TypeID form, plus the pair it decodes from.
type devtoolID struct {
	Prefix string `json:"prefix"`
	UUID   string `json:"uuid"`
	ID     string `json:"id"`
}

// encodeID turns a prefix and a UUID into the TypeID form the API prints.
func encodeID(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Prefix string `json:"prefix"`
		UUID   string `json:"uuid"`
	}
	if err := json.UnmarshalRead(r.Body, &req); err != nil {
		responder.BadRequestJSON(w, r, "the body must be JSON with a prefix and a uuid")
		return
	}

	id, err := typeid.FromUUIDWithPrefix(req.Prefix, req.UUID)
	if err != nil {
		responder.BadRequestJSON(w, r, err.Error())
		return
	}

	responder.Success(w, r, http.StatusOK, devtoolID{
		Prefix: id.Prefix(), UUID: req.UUID, ID: id.String(),
	}, responder.WithMessage("the type id was encoded"))
}

// decodeID turns a TypeID string back into the prefix and UUID it carries.
func decodeID(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.UnmarshalRead(r.Body, &req); err != nil {
		responder.BadRequestJSON(w, r, "the body must be JSON with an id")
		return
	}

	id, err := typeid.FromString(req.ID)
	if err != nil {
		responder.BadRequestJSON(w, r, err.Error())
		return
	}

	responder.Success(w, r, http.StatusOK, devtoolID{
		Prefix: id.Prefix(), UUID: id.UUID(), ID: id.String(),
	}, responder.WithMessage("the type id was decoded"))
}

// devtoolPage renders one of the dohttp pages, whose only failure mode is a
// template error the caller cannot answer with content.
func devtoolPage(w http.ResponseWriter, html string, err error) {
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(html))
}
