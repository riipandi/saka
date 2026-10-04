package oauthsso

import (
	"context"
	"encoding/json/v2"
	"log/slog"
	"strings"

	"uuid"

	"github.com/riipandi/saka/internal/datastore"
	"github.com/riipandi/saka/modules/identity/user"
)

// profileApplier is the account-write seam the resolution's refresh runs
// through: the user module owns the account row, this feature only hands
// it the answers the mapping produced. The user service satisfies it.
type profileApplier interface {
	// ApplyProviderProfile rewrites the profile fields the source
	// answered for, inside the caller's transaction.
	ApplyProviderProfile(ctx context.Context, db datastore.Querier, userID uuid.UUID, profile user.ProviderProfile) error
	// MergeCustomAttributes folds the source's answered attributes onto
	// the account's document, inside the caller's transaction.
	MergeCustomAttributes(ctx context.Context, db datastore.Querier, userID uuid.UUID, attrs map[string]any) error
	// RefreshProviderPicture stores the picture bytes the source
	// answered — the write path beside the transaction, a failed refresh
	// never failing the sign-in.
	RefreshProviderPicture(ctx context.Context, userID uuid.UUID, data []byte) error
}

// WithProfiles wires the account-write seam. Nil skips the refresh —
// the state a bare wiring runs in, the sign-in still completing.
func (s *Service) WithProfiles(applier profileApplier) *Service {
	s.profiles = applier
	return s
}

// avatarMaxBytes is the picture a provider may hand over: a bigger body
// is not a picture the account keeps, and reading one to the end would
// be the provider's way to spend our bandwidth.
const avatarMaxBytes = 4 << 20 // 4 MiB

// applyResolutionProfile carries the mapping's answers onto the account
// inside the resolution's transaction: the names the flow resolved and
// the custom attributes the profile document answers. The username is
// not here — it is written once, at the JIT creation — and the picture
// is not either — its write path runs beside the transaction.
func (s *Service) applyResolutionProfile(ctx context.Context, tx datastore.Querier, flow Flow, conn Connection, userID uuid.UUID) error {
	if s.profiles == nil {
		return nil
	}
	if err := s.profiles.ApplyProviderProfile(ctx, tx, userID, user.ProviderProfile{
		GivenName:  flow.GivenName,
		FamilyName: flow.FamilyName,
	}); err != nil {
		return err
	}
	return s.profiles.MergeCustomAttributes(ctx, tx, userID,
		customAttributeDocument(flow.Profile, conn.CustomAttributes, s.log))
}

// refreshAvatar carries the mapped picture onto the account: the URL the
// flow resolved is fetched over the outbound client and the bytes stored
// through the account seam. Every failure — no client wired, a dead
// fetch, a body that is no image — is the refresh this run could not
// make: logged, the stored picture kept, the sign-in never failed.
func (s *Service) refreshAvatar(ctx context.Context, flow Flow, userID uuid.UUID) {
	if s.profiles == nil || s.fetcher == nil || flow.AvatarURL == "" {
		return
	}
	status, body, err := s.fetcher.Do(ctx, flow.AvatarURL)
	if err != nil {
		s.log.WarnContext(ctx, "oauthsso: the avatar download failed; the stored picture stays",
			slog.String("user_id", userID.String()), slog.String("avatar_url", flow.AvatarURL), slog.String("error", err.Error()))
		return
	}
	if status != 200 {
		s.log.WarnContext(ctx, "oauthsso: the avatar download answered a non-200; the stored picture stays",
			slog.String("user_id", userID.String()), slog.String("avatar_url", flow.AvatarURL), slog.Int("status", status))
		return
	}
	if len(body) > avatarMaxBytes {
		s.log.WarnContext(ctx, "oauthsso: the avatar download is too large; the stored picture stays",
			slog.String("user_id", userID.String()), slog.String("avatar_url", flow.AvatarURL), slog.Int("bytes", len(body)))
		return
	}
	if err := s.profiles.RefreshProviderPicture(ctx, userID, body); err != nil {
		s.log.WarnContext(ctx, "oauthsso: the avatar could not be stored; the stored picture stays",
			slog.String("user_id", userID.String()), slog.String("error", err.Error()))
	}
}

// customAttributeDocument reads the claims the connection's custom
// attributes name off the raw profile document. An attribute whose claim
// the provider does not answer — or answers blank — lands nothing, and
// the stored value keeps its place; a structured answer (an object, or
// an array carrying one) is refused and lands nothing either; a scalar
// or an array of scalars lands as it arrived.
func customAttributeDocument(profile []byte, defs []CustomAttribute, log *slog.Logger) map[string]any {
	if len(defs) == 0 || len(profile) == 0 {
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal(profile, &raw); err != nil {
		return nil
	}
	out := make(map[string]any, len(defs))
	for _, def := range defs {
		value, present := raw[def.Claim]
		if !present || blankClaim(value) {
			continue
		}
		if !landableClaim(value) {
			log.WarnContext(context.Background(), "oauthsso: a custom attribute's answer is structured; it lands nothing",
				slog.String("key", def.Key), slog.String("claim", def.Claim))
			continue
		}
		out[def.Key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// blankClaim reports an answer that carries no value: absent, null, a
// whitespace-only string, or an empty array.
func blankClaim(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	case []any:
		return len(typed) == 0
	default:
		return false
	}
}

// landableClaim reports an answer the account document may hold: a
// scalar, or an array whose every member is a scalar. An object — and an
// array that carries one — is the shape the column refuses.
func landableClaim(value any) bool {
	switch typed := value.(type) {
	case string, bool, float64:
		return true
	case []any:
		for _, member := range typed {
			switch member.(type) {
			case string, bool, float64:
			default:
				return false
			}
		}
		return true
	default:
		return false
	}
}
