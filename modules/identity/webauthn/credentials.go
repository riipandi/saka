package webauthn

import (
	"context"
	"errors"

	"uuid"

	"github.com/riipandi/tango/internal/audit"
	"go.jetify.com/typeid"
)

// typeidParseCredential reads a credential's wire form back into its UUID.
// A wire form that does not parse names no row this package owns.
func typeidParseCredential(wire string) (uuid.UUID, error) {
	parsed, err := typeid.Parse[CredentialID](wire)
	if err != nil {
		return uuid.UUID{}, err
	}
	id, err := uuid.Parse(parsed.UUID())
	if err != nil {
		return uuid.UUID{}, err
	}
	return id, nil
}

// ListCredentials answers the account's roll, oldest first — the order a
// settings page renders.
func (s *Service) ListCredentials(ctx context.Context, userID uuid.UUID) ([]View, error) {
	rows, err := s.repo.ListCredentials(ctx, s.pool, userID)
	if err != nil {
		return nil, err
	}
	out := make([]View, 0, len(rows))
	for _, row := range rows {
		shaped, err := view(row)
		if err != nil {
			return nil, err
		}
		out = append(out, shaped)
	}
	return out, nil
}

// RenameCredential replaces one credential's display name. The credential
// must be the caller's own: another account's answers not-found, the same
// refusal an unknown identifier does, so the surface does not disclose
// which accounts hold which credentials.
func (s *Service) RenameCredential(ctx context.Context, userID uuid.UUID, credentialWire, name string) (View, error) {
	return s.renameCredential(ctx, userID, credentialWire, name, audit.EventWebauthnCredentialRenamed)
}

// renameCredential is the rename both the holder's and the administrator's
// surface run; the event says who it was.
func (s *Service) renameCredential(ctx context.Context, userID uuid.UUID, credentialWire, name, event string) (View, error) {
	id, err := typeidParseCredential(credentialWire)
	if err != nil {
		return View{}, ErrCredentialForeign
	}
	row, err := s.repo.GetCredentialByID(ctx, s.pool, id)
	if errors.Is(err, ErrNoRows) {
		return View{}, ErrCredentialForeign
	}
	if err != nil {
		return View{}, err
	}
	if row.UserID != userID {
		return View{}, ErrCredentialForeign
	}

	if err := s.repo.RenameCredential(ctx, s.pool, row.ID, name); err != nil {
		return View{}, err
	}
	row.Name = name

	s.audit.Record(ctx, s.pool, audit.Entry{
		Event:        event,
		UserID:       userID.String(),
		ResourceType: "webauthn_credential",
		ResourceID:   row.ID.String(),
		Payload:      map[string]string{"credential": credentialWire, "name": name},
	})

	return view(row)
}

// DeleteCredential removes one credential. Deleting the last credential on
// an account that still holds a password is allowed — the password is the
// way back in. An account whose password row is gone (or never existed)
// would be stranded by the removal, and the refusal says so: the recovery
// anchor is the plan's invariant, held here at the last door.
func (s *Service) DeleteCredential(ctx context.Context, userID uuid.UUID, credentialWire string) error {
	return s.deleteCredential(ctx, userID, credentialWire, audit.EventWebauthnCredentialRemoved)
}

// deleteCredential is the removal both the holder's and the administrator's
// surface run; the event says who it was.
func (s *Service) deleteCredential(ctx context.Context, userID uuid.UUID, credentialWire, event string) error {
	id, err := typeidParseCredential(credentialWire)
	if err != nil {
		return ErrCredentialForeign
	}
	row, err := s.repo.GetCredentialByID(ctx, s.pool, id)
	if errors.Is(err, ErrNoRows) {
		return ErrCredentialForeign
	}
	if err != nil {
		return err
	}
	if row.UserID != userID {
		return ErrCredentialForeign
	}

	held, err := s.repo.CountCredentials(ctx, s.pool, userID)
	if err != nil {
		return err
	}
	if held <= 1 {
		// The password lookup doubles as the stranded check: the issuer's
		// read joins the password table, so an account without a password
		// row answers not-found, and this would be its last way in.
		if _, pwErr := s.issuer.FindAccountByID(ctx, userID); errors.Is(pwErr, ErrNoRows) {
			return ErrLastWayIn
		}
	}

	deleted, err := s.repo.DeleteCredential(ctx, s.pool, row.ID)
	if err != nil {
		return err
	}
	if !deleted {
		return ErrCredentialForeign
	}

	s.audit.Record(ctx, s.pool, audit.Entry{
		Event:        event,
		UserID:       userID.String(),
		ResourceType: "webauthn_credential",
		ResourceID:   row.ID.String(),
		Payload:      map[string]string{"credential": credentialWire, "name": row.Name},
	})
	return nil
}
