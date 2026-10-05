//go:build !debug

// The release counterpart of the passkey probe pages: nothing is carried.
// The devtool surface is the debug build's — a release binary refuses the
// paths with the 404 envelope (devtool_release.go), and the map it serves
// stays empty so the binary carries none of the pages.

package web

// WebauthnProbePages is empty in a release build.
var WebauthnProbePages = map[string]string{}
