// The SPA completion dialect: the interaction continuation's success is a
// redirect the provider writes to the RP's redirect_uri, and a browser tab
// the SPA drives can neither read a 302's Location (a fetch's manual
// redirect is opaque) nor navigate while carrying the Authorization header.
// A request that opts in through the dialect header gets the redirect
// wrapped into the interaction dialect's JSON instead, and the SPA
// navigates itself. The header is opt-in — the protocol's own callers (the
// conformance suite's browser, the REST driver, curl) keep the raw 302 —
// so nothing outside the three callback paths the SPA resumes at can tell
// the wrapper exists.
package oidc

import (
	"net/http"
	"strings"
)

// spaDialectHeader opts a callback call into the SPA completion dialect;
// spaDialectValue is the only value that arms it.
const (
	spaDialectHeader = "X-Saka-Interaction"
	spaDialectValue  = "spa"
)

// spaDialect wraps the provider handler on the SPA-facing callback paths.
// A dialect request whose answer is a redirect is answered as the
// interaction dialect's JSON; everything else — every path, every status,
// every headerless request — passes through byte-for-byte.
func spaDialect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get(spaDialectHeader), spaDialectValue) {
			next.ServeHTTP(w, r)
			return
		}
		capture := &spaRedirectCapture{ResponseWriter: w}
		next.ServeHTTP(capture, r)
		if capture.redirected {
			writeInteraction(w, http.StatusOK, map[string]any{"redirect": capture.location})
		}
	})
}

// spaRedirectCapture holds a written redirect back from the wire: the
// status and body a redirect carries belong to the dialect, not to the
// caller that asked for it. A non-redirect answer streams untouched.
type spaRedirectCapture struct {
	http.ResponseWriter
	redirected bool
	location   string
}

func (c *spaRedirectCapture) WriteHeader(status int) {
	if c.redirected {
		return
	}
	if status >= http.StatusMultipleChoices && status < http.StatusBadRequest {
		if location := c.Header().Get("Location"); location != "" {
			c.redirected = true
			c.location = location
			// The headers a redirect answer carried are not the JSON
			// answer's: the Location and the redirect body's content
			// type and length belong to the answer the dialect replaces.
			c.Header().Del("Location")
			c.Header().Del("Content-Length")
			return
		}
	}
	c.ResponseWriter.WriteHeader(status)
}

func (c *spaRedirectCapture) Write(p []byte) (int, error) {
	if c.redirected {
		// The redirect's HTML body is for a browser that navigates; the
		// dialect caller reads the JSON written after the handler returns.
		return len(p), nil
	}
	return c.ResponseWriter.Write(p)
}
