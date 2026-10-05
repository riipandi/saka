package webauthn

import (
	"testing"

	"github.com/riipandi/saka/pkg/testutils/softauthn"
)

// The soft authenticator is the test's device. The construction lives in
// pkg/testutils/softauthn so the wire-level E2E driver speaks the same
// ceremonies through the same instrument; this file is the adapter the
// package's tests drive.
type SoftAuthenticator struct {
	device *softauthn.Authenticator
}

// NewSoftAuthenticator builds a device holding one fresh credential.
func NewSoftAuthenticator(backupEligible, backupState, userVerification bool) *SoftAuthenticator {
	return &SoftAuthenticator{device: softauthn.New(backupEligible, backupState, userVerification)}
}

// CredentialID exposes the credential's raw identifier.
func (a *SoftAuthenticator) CredentialID() []byte { return a.device.CredentialID() }

// RewindCounter sets the next assertion's counter to zero — the clone test's
// move.
func (a *SoftAuthenticator) RewindCounter() { a.device.RewindCounter() }

// Create answers the registration ceremony.
func (a *SoftAuthenticator) Create(t *testing.T, options, origin string) string {
	t.Helper()
	response, err := a.device.Create(options, origin)
	if err != nil {
		t.Fatalf("softauthn: create: %v", err)
	}
	return response
}

// Get answers the authentication ceremony.
func (a *SoftAuthenticator) Get(t *testing.T, options, origin string, userHandle []byte) string {
	t.Helper()
	response, err := a.device.Get(options, origin, userHandle)
	if err != nil {
		t.Fatalf("softauthn: get: %v", err)
	}
	return response
}
