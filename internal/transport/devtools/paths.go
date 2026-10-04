package devtools

// The devtool paths both builds answer: a debug build serves the instruments,
// a release build refuses the paths with a 404 envelope rather than letting
// the SPA claim them.

// devtoolUIPath is where the samber/do web UI mounts.
const devtoolUIPath = "/debug/do"

// webauthnProbePath is where the WebAuthn probe page mounts — the debug
// build's browser instrument.
const webauthnProbePath = "/debug/webauthn-probe"
