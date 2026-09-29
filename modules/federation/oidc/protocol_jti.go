package oidc

import (
	"context"
	"errors"
)

// jtiReplayWindowSeconds is how long one JWT ID stays claimed in
// oauth2_jtis. The library's consumer answers only the jti itself — no
// expiry rides the call — so the window must outlast the lifetime of
// every JWT whose jti it sees: the DPoP proofs and client assertion
// JWTs the endpoints validate, whose claims the library caps well under
// a minute of clock skew. Fifteen minutes dwarfs that and keeps the
// table's population bounded by the request rate, not by history.
const jtiReplayWindowSeconds = 15 * 60

// ErrJTIReplayed is a jti presented a second time inside its window.
// The library's call sites refuse any non-ErrNotFound error from the
// consumer, so the replay surfaces as the request's own rejection —
// invalid client or invalid request, per the endpoint that met it.
var ErrJTIReplayed = errors.New("oidc: the jti was already presented")

// consumeJTI claims one JWT ID for its first presentation: the INSERT
// wins or the replay loses, and two concurrent claims race over the
// unique index, so a replayed JWT is refused without a read-then-write
// window. The row's expiry is the claim's retention, not the token's —
// the consumer never sees the presented token's lifetime — and the
// protocol cleanup sweep reaps what passes.
func (st protocolStore) consumeJTI(ctx context.Context, jti string) error {
	tag, err := st.pool.Exec(ctx,
		`INSERT INTO public.oauth2_jtis (jti, expires_at)
		 VALUES ($1, now() + make_interval(secs => $2))
		 ON CONFLICT (jti) DO NOTHING`,
		jti, jtiReplayWindowSeconds)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrJTIReplayed
	}
	return nil
}
