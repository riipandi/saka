#!/usr/bin/env python3
"""Walk the OAuth browser flow: Tango start -> the mock's authorize (the
login page takes a username and nothing else) -> the callback -> the SPA
redirect. Prints the flow token, or the error word the SPA would show.

Usage: walk.py <issuer-port> <provider-slug> <subject>
"""
import re
import sys
import urllib.parse as up
import urllib.request as req
import urllib.error as err
import http.cookiejar as cj


class NoRedirect(req.HTTPRedirectHandler):
    def redirect_request(self, *a, **k):
        return None


jar = cj.CookieJar()
opener = req.build_opener(NoRedirect, req.HTTPCookieProcessor(jar))


def open302(request):
    try:
        r = opener.open(request, timeout=10)
        return r.status, r.headers, r.read()
    except err.HTTPError as e:
        return e.code, e.headers, e.read()


port, provider, subject = sys.argv[1:4]

# 1. The start route answers the authorize URL as a 302.
status, headers, _ = open302(f"http://localhost:3080/api/oauth/{provider}/start")
authorize_url = headers.get("Location", "")
if status != 302 or not authorize_url:
    raise SystemExit(f"start did not redirect: {status} {authorize_url}")

# 2/3. The login page posts the username alone — the mock derives the
# whole identity from the subject through the config's mappings. The
# form carries no action, so the post lands on the authorize URL with
# its query intact — the params the mock re-reads.
status, headers, body = open302(authorize_url)
if status != 200:
    raise SystemExit(f"authorize page unusable: {status} {body[:200]}")
form = up.urlencode({"username": subject}).encode()
status, headers, _ = open302(req.Request(authorize_url, data=form))
callback_url = headers.get("Location", "")
if status != 302 or "code=" not in callback_url:
    raise SystemExit(f"login refused: {status} {callback_url[:160]}")

# 4. The callback answers the SPA redirect with the flow token.
status, headers, _ = open302(callback_url)
landing = headers.get("Location", "")
qs = dict(up.parse_qsl(up.urlsplit(landing).query))
if status != 302 or "flow_token" not in qs:
    raise SystemExit(f"callback failed: {status} {landing}")
print(qs["flow_token"])
