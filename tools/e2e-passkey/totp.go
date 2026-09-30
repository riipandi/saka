package main

import (
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // SHA-1 is RFC 6238's algorithm, not a security choice here
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

// totpCode renders the six-digit value the authenticator shows at now —
// the RFC 6238 SHA-1 default, thirty-second steps.
func totpCode(base32Secret string, at time.Time) string {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimRight(base32Secret, "=")))
	if err != nil {
		fail("the TOTP secret does not decode: %v", err)
	}
	step := uint64(at.Unix()) / 30 //nolint:gosec // the clock's step count, nowhere near the boundary
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], step)
	mac := hmac.New(sha1.New, key)
	mac.Write(counter[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := (binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff) % 1_000_000
	return fmt.Sprintf("%06d", value)
}

func timeNow() time.Time { return time.Now() }

// totpClock is the stepping clock the run's TOTP completions share. A code
// is single-use per its thirty-second step — the replay is the
// compare-and-set the row holds — so every completion takes a step the row
// has never seen. The step walks forward only when the wall clock has not
// already moved past it: a code too far from the verifier's window is as
// invalid as a replayed one.
type totpClock struct {
	secret string
	step   uint64
}

func newTotpClock(secret string) *totpClock {
	return &totpClock{secret: secret, step: uint64(time.Now().Unix()) / 30} //nolint:gosec // the clock's step count, nowhere near the boundary
}

func (c *totpClock) next() string {
	nowStep := uint64(time.Now().Unix()) / 30 //nolint:gosec // the clock's step count, nowhere near the boundary
	// When the run has spent the step the clock is still in, the only
	// honest code is the next boundary's — wait for the wall clock to
	// carry it into the verifier's one-step window.
	if c.step >= nowStep+1 {
		time.Sleep(time.Until(time.Unix(int64(c.step+1)*30, 0)) + 2*time.Second) //nolint:gosec // the step boundary, nowhere near the limit
		nowStep = uint64(time.Now().Unix()) / 30                                 //nolint:gosec // the clock's step count, nowhere near the boundary
	}
	if nowStep > c.step {
		c.step = nowStep
	} else {
		c.step++
	}
	return totpCode(c.secret, time.Unix(int64(c.step)*30, 0)) //nolint:gosec // the step boundary, nowhere near the limit
}
