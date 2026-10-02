#!/usr/bin/env python3
"""The OAuth SSO E2E ladder against a running release binary and the
navikt mock (docker/compose-mock.yaml). Every branch the plan names,
over the wire, ending with the created rows removed.
"""
import base64
import hashlib
import hmac
import json
import struct
import sys
import time
import urllib.request as req
import urllib.error as err

BASE = "http://localhost:3080"
PASS = []
FAIL = []


def check(name, cond, detail=""):
    (PASS if cond else FAIL).append(name)
    print(f"  {'PASS' if cond else 'FAIL'}  {name}" + (f"  — {detail}" if (detail and not cond) else ""))


def rpc(method, body, token=None, reauth=None):
    request = req.Request(
        f"{BASE}/rpc/{method}",
        data=json.dumps(body).encode(),
        headers={
            "Content-Type": "application/json",
            "Connect-Protocol-Version": "1",
            **({"Authorization": f"Bearer {token}"} if token else {}),
            **({"X-Tango-Reauthentication": reauth} if reauth else {}),
        },
        method="POST",
    )
    try:
        with req.urlopen(request, timeout=30) as r:
            return r.status, json.loads(r.read())
    except err.HTTPError as e:
        return e.code, json.loads(e.read())


def walk(port, provider, subject):
    import urllib.parse as up
    import http.cookiejar as cj

    class NoRedirect(req.HTTPRedirectHandler):
        def redirect_request(self, *a, **k):
            return None

    jar = cj.CookieJar()
    opener = req.build_opener(NoRedirect, req.HTTPCookieProcessor(jar))

    def open302(request):
        try:
            r = opener.open(request, timeout=30)
            return r.status, r.headers, r.read()
        except err.HTTPError as e:
            return e.code, e.headers, e.read()

    status, headers, _ = open302(f"{BASE}/api/oauth/{provider}/start")
    authorize = headers.get("Location", "")
    assert status == 302 and authorize, "start refused"
    status, headers, body = open302(authorize)
    assert status == 200, "authorize page refused"
    form = up.urlencode({"username": subject}).encode()
    status, headers, _ = open302(req.Request(authorize, data=form))
    callback = headers.get("Location", "")
    assert status == 302 and "code=" in callback, f"login refused: {status} {callback[:120]}"
    status, headers, _ = open302(callback)
    landing = headers.get("Location", "")
    assert status == 302, f"callback failed: {landing[:120]}"
    qs = dict(up.parse_qsl(up.urlsplit(landing).query))
    assert "flow_token" in qs, f"no flow token: {landing[:120]}"
    return qs["flow_token"]


def totp(secret_b32, at=None):
    key = base64.b32decode(secret_b32 + "=" * (-len(secret_b32) % 8))
    step = int((at or time.time()) / 30)
    digest = hmac.new(key, struct.pack(">Q", step), hashlib.sha1).digest()
    offset = digest[-1] & 0x0F
    code = (struct.unpack(">I", digest[offset:offset + 4])[0] & 0x7FFFFFFF) % 1_000_000
    return f"{code:06d}"


def mailpit_clear():
    """Drop every stored message, so the code a step reads is the one its
    own walk sent — a previous run's email is a stale answer."""
    request = req.Request("http://localhost:8025/api/v1/messages", method="DELETE")
    try:
        req.urlopen(request, timeout=10)
    except err.HTTPError:
        pass


def mailpit_code(recipient):
    with req.urlopen("http://localhost:8025/api/v1/messages?limit=10", timeout=10) as r:
        messages = json.loads(r.read())
    for message in messages.get("messages", []):
        addresses = [a["Address"] for a in message.get("To", [])]
        if recipient in addresses:
            with req.urlopen(f"http://localhost:8025/api/v1/message/{message['ID']}", timeout=10) as r:
                full = json.loads(r.read())
            html = full.get("HTML", "")
            # the code renders in the one styled span; the closing tag is
            # the boundary that keeps the style block's CSS out
            match = __import__("re").search(
                r'<span[^>]*letter-spacing[^>]*>\s*([A-Za-z0-9]+)\s*</span>', html, __import__("re").S)
            if match:
                return match.group(1)
    return None


admin = open("/tmp/tango-token").read().strip()

print("0. Pre-clean: the accounts a previous ladder run left behind")
_, users = rpc("tango.identity.v1.UserService/ListUsers", {}, admin)
for user in users.get("users", []):
    if user.get("email", "").endswith("hogwarts.example"):
        rpc("tango.identity.v1.UserService/DeleteUser", {"id": user["id"]}, admin)

print("1. JIT sign-in, open mode, the names step (github sim)")
_, conn = rpc("tango.authn.v1.OAuthSSOService/ListConnections", {}, admin)
conns = conn["connections"]
github = next(c for c in conns if c["provider"] == "ngauth-github")
google = next(c for c in conns if c["provider"] == "ngauth-google")

flow = walk("3221", "ngauth-github", "alice")
status, answer = rpc("tango.authn.v1.OAuthSSOService/ContinueSignIn", {"flow_token": flow})
check("1a. the names pause answers", answer.get("stage") == "require_names", str(answer))
status, answer = rpc("tango.authn.v1.OAuthSSOService/ContinueSignIn",
                     {"flow_token": flow, "given_name": "Alice", "family_name": "Hogwarts"})
alice_token = answer.get("access_token", "")
alice_id = answer.get("user", {}).get("id", "")
check("1b. the JIT account opens a session",
      answer.get("status") == "success" and bool(alice_token),
      str(answer.get("message", answer))[:160])
check("1c. the derived username and display name", answer.get("user", {}).get("display_name") == "Alice Hogwarts",
      str(answer.get("user")))

print("2. The binding signs the same provider identity in without the names step")
flow = walk("3221", "ngauth-github", "alice")
status, answer = rpc("tango.authn.v1.OAuthSSOService/ContinueSignIn", {"flow_token": flow})
check("2a. the binding branch answers a session",
      answer.get("status") == "success" and answer.get("user", {}).get("id") == alice_id,
      str(answer)[:160])

print("3. Linking on a verified email (google sim, account pre-exists)")
status, answer = rpc("tango.identity.v1.UserService/CreateUser", {
    "username": "sophie", "email": "sophie@hogwarts.example", "password": "Vetra#CERN-4782x9", "first_name": "Sophie", "last_name": "Hogwarts"
}, admin)
check("3a. the account the address names exists", status == 200, str(answer)[:160])
flow = walk("3220", "ngauth-google", "sophie")
status, answer = rpc("tango.authn.v1.OAuthSSOService/ContinueSignIn", {"flow_token": flow})
sophie_linked = answer.get("status") == "success"
check("3b. the verified address links and signs in", sophie_linked, str(answer)[:160])

print("4. The unverified email gate and the code (google sim)")
mailpit_clear()
flow = walk("3220", "ngauth-google", "unverified-bob")
status, answer = rpc("tango.authn.v1.OAuthSSOService/ContinueSignIn", {"flow_token": flow})
check("4a. the flow pauses at verify_email", answer.get("stage") == "verify_email", str(answer))
code = None
for _ in range(30):
    code = mailpit_code("unverified-bob@hogwarts.example")
    if code:
        break
    time.sleep(2)
check("4b. the code arrived", bool(code), "no mailpit message")
status, answer = rpc("tango.authn.v1.OAuthSSOService/VerifySignInEmail",
                     {"flow_token": flow, "code": "WRONGCODE12"})
check("4c. a wrong code answers invalid_argument", status == 400, str(answer)[:120])
status, answer = rpc("tango.authn.v1.OAuthSSOService/VerifySignInEmail",
                     {"flow_token": flow, "code": code or "AAAAAAAA12"})
check("4d. the right code moves the flow on", answer.get("stage") == "resolved", str(answer))
status, answer = rpc("tango.authn.v1.OAuthSSOService/ContinueSignIn",
                     {"flow_token": flow, "given_name": "Bob", "family_name": "Hogwarts"})
check("4e. the proven address JIT-creates the account", answer.get("status") == "success", str(answer)[:160])

print("5. The continue step for a provider that named nobody (google sim)")
flow = walk("3220", "ngauth-google", "nonames-carl")
status, answer = rpc("tango.authn.v1.OAuthSSOService/ContinueSignIn", {"flow_token": flow})
check("5a. the names pause", answer.get("stage") == "require_names", str(answer))
status, answer = rpc("tango.authn.v1.OAuthSSOService/ContinueSignIn",
                     {"flow_token": flow, "given_name": "Carl", "family_name": "Hogwarts"})
check("5b. the collected names complete the sign-in", answer.get("status") == "success", str(answer)[:160])

print("6. The stranding refusal behind the step-up gate (alice: no password)")
_, linked = rpc("tango.authn.v1.OAuthSSOService/ListLinkedConnections", {}, alice_token)
check("6a. the ledger answers the binding", len(linked.get("linked_accounts", [])) == 1, str(linked)[:160])
binding_id = linked["linked_accounts"][0]["id"]
status, answer = rpc("tango.authn.v1.OAuthSSOService/UnlinkConnection",
                     {"linked_account_id": binding_id}, alice_token)
check("6b. the unproven caller is refused first", status == 401, f"{status} {str(answer)[:120]}")
mailpit_clear()
status, answer = rpc("tango.authn.v1.WebAuthnService/SendReauthenticationCode", {}, alice_token)
code = None
for _ in range(30):
    code = mailpit_code("alice@hogwarts.example")
    if code:
        break
    time.sleep(2)
check("6c. the reverification code arrived", bool(code), "no mailpit message")
status, answer = rpc("tango.authn.v1.WebAuthnService/Reauthenticate",
                     {"email_code": code or "AAAAAAAA12"}, alice_token)
proof = answer.get("token", "")
check("6d. the proof is answered once", bool(proof), str(answer)[:120])
status, answer = rpc("tango.authn.v1.OAuthSSOService/UnlinkConnection",
                     {"linked_account_id": binding_id}, alice_token,
                     reauth=proof)
check("6e. the last credential is refused", status == 400 and "password" in answer.get("message", ""),
      f"{status} {str(answer)[:120]}")

print("7. The MFA bridge after OAuth (alice)")
status, answer = rpc("tango.authn.v1.MultifactorService/BeginTotpEnrollment", {"name": "Tower"}, alice_token)
secret = answer.get("secret", "")
totp_id = answer.get("totp_id", "")
check("7a. the enrollment starts", bool(secret) and bool(totp_id), str(answer)[:120])
status, answer = rpc("tango.authn.v1.MultifactorService/ConfirmTotpEnrollment",
                     {"totp_id": totp_id, "code": totp(secret)}, alice_token)
check("7b. the factor confirms", status == 200, str(answer)[:120])
flow = walk("3221", "ngauth-github", "alice")
status, answer = rpc("tango.authn.v1.OAuthSSOService/ContinueSignIn", {"flow_token": flow})
bridge = answer.get("mfa_pending_token", "")
check("7c. the fork answers the bridge, never a session",
      answer.get("mfa_required") is True and bool(bridge), str(answer)[:160])
status, answer = rpc("tango.authn.v1.MultifactorService/CompleteSignIn",
                     {"pending_token": bridge, "code": totp(secret)}, alice_token)
check("7d. the second factor opens the session", answer.get("status") == "success", str(answer)[:160])

print("8. Invite mode closes the JIT door")
_, _ = rpc("tango.settings.v1.SettingsService/Update", {"key": "access.mode", "value": "invite"}, admin)
flow = walk("3220", "ngauth-google", "mallory")
status, answer = rpc("tango.authn.v1.OAuthSSOService/ContinueSignIn", {"flow_token": flow})
check("8a. the JIT creation is refused", status == 403, f"{status} {str(answer)[:120]}")
rpc("tango.settings.v1.SettingsService/Update", {"key": "access.mode", "value": "open"}, admin)

print("9. Cleanup: the rows the ladder created leave in the same turn")
_, users = rpc("tango.identity.v1.UserService/ListUsers", {"search": "hogwarts.example"}, admin)
for user in users.get("users", []):
    if user.get("email", "").endswith("hogwarts.example"):
        rpc("tango.identity.v1.UserService/DeleteUser", {"id": user["id"]}, admin)
_, users = rpc("tango.identity.v1.UserService/ListUsers", {"search": "hogwarts.example"}, admin)
leftovers = [u["id"] for u in users.get("users", [])
             if u.get("email", "").endswith("hogwarts.example")]
check("9a. the E2E accounts are gone", not leftovers, str(leftovers))

print()
print(f"ladder: {len(PASS)} passed, {len(FAIL)} failed")
if FAIL:
    print("failed:", ", ".join(FAIL))
    sys.exit(1)
