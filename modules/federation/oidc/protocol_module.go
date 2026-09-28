package oidc

import (
	"github.com/go-chi/chi/v5"
)

// protocolModule is the federation area's protocol feature: the OIDC
// provider the /oidc surface serves. It owns no ConnectRPC procedures —
// the wire protocol is the specifications' own.
type protocolModule struct {
	protocol *Protocol
}

// NewProtocolModule builds the protocol feature over its wired provider.
func NewProtocolModule(protocol *Protocol) *protocolModule {
	return &protocolModule{protocol: protocol}
}

// Name reports the feature the composition logs.
func (m *protocolModule) Name() string { return "oidc-protocol" }

// Mount registers the provider's paths.
func (m *protocolModule) Mount(r chi.Router) {
	m.protocol.Mount(r)
}
