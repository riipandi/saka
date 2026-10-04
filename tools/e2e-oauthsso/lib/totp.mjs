// The six-digit answer a TOTP authenticator renders right now — the
// ladder's stand-in for the app on the account's phone. The secret is
// the Base32 string the enrollment answered. `otpauth` owns the
// decoding and the HMAC — the ladder keeps no hand-rolled cryptography.
import { TOTP } from 'otpauth'

export function totp(secretB32) {
  return new TOTP({
    secret: secretB32,
    digits: 6,
    period: 30,
    algorithm: 'SHA1'
  }).generate()
}
