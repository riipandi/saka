package user

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"uuid"

	"github.com/riipandi/saka/internal/datastore"
)

// ProviderProfile is the profile facts an identity source answered for
// an account. An empty field is an answer the source gave nobody — the
// stored value keeps its place.
type ProviderProfile struct {
	GivenName  string
	FamilyName string
}

// ApplyProviderProfile rewrites the profile fields an identity source
// answered for, inside the caller's transaction: a name the source
// carries replaces the stored one, and the display name the pair
// composes into follows — but only when a name actually moved, so the
// display name an account's owner wrote survives a provider that keeps
// answering the same names. A source that names nobody changes nothing.
func (s *Service) ApplyProviderProfile(ctx context.Context, db datastore.Querier, userID uuid.UUID, profile ProviderProfile) error {
	row, err := s.repo.GetUser(ctx, db, userID)
	if errors.Is(err, datastore.ErrNoRows) {
		return ErrUserNotFound
	}
	if err != nil {
		return fmt.Errorf("user: read for provider profile: %w", err)
	}

	// Only a name that actually moved is written, and the display name
	// follows only then: a provider that keeps answering the same names
	// costs no write, and the display name an account's owner wrote —
	// when the source answers none it is not derived from — stays.
	changed := false
	if profile.GivenName != "" && profile.GivenName != row.FirstName {
		row.FirstName = profile.GivenName
		changed = true
	}
	if profile.FamilyName != "" && profile.FamilyName != row.LastName {
		row.LastName = profile.FamilyName
		changed = true
	}
	if !changed {
		return nil
	}
	if name := displayName(row.FirstName, row.LastName); name != "" {
		row.DisplayName = name
	}

	if _, err := s.repo.UpdateUser(ctx, db, row); err != nil {
		return fmt.Errorf("user: apply provider profile: %w", err)
	}
	return nil
}

// MergeCustomAttributes folds the attributes an identity source answered
// onto the account's document, inside the caller's transaction. The
// merge is shallow: a key the document carries replaces the stored one,
// a key it does not carry keeps the stored value, and an empty set
// changes nothing.
func (s *Service) MergeCustomAttributes(ctx context.Context, db datastore.Querier, userID uuid.UUID, attrs map[string]any) error {
	if len(attrs) == 0 {
		return nil
	}
	if err := s.repo.MergeCustomAttributes(ctx, db, userID, attrs); err != nil {
		return fmt.Errorf("user: merge custom attributes: %w", err)
	}
	return nil
}

// RefreshProviderPicture stores the bytes an identity source answered as
// the account's picture — the same pipeline the account's own upload
// runs, the kind decided by the magic bytes and the replaced object
// deleted first. A run without the storage engine refuses; the caller
// treats that refusal as the refresh it cannot make, not a failed
// sign-in.
func (s *Service) RefreshProviderPicture(ctx context.Context, userID uuid.UUID, data []byte) error {
	if s.pictures == nil {
		return ErrPicturesUnavailable
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return ErrUnsupportedPicture
	}
	row, err := s.repo.GetUser(ctx, s.pool, userID)
	if errors.Is(err, datastore.ErrNoRows) {
		return ErrUserNotFound
	}
	if err != nil {
		return fmt.Errorf("user: read for provider picture: %w", err)
	}
	return s.storePicture(ctx, userID, row, data)
}

// displayName joins a given and a family name the way the account view
// renders them; an empty pair composes nothing.
func displayName(given, family string) string {
	switch {
	case given != "" && family != "":
		return given + " " + family
	case given != "":
		return given
	case family != "":
		return family
	default:
		return ""
	}
}
