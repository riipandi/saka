/**
 * The frontend's view of the settings catalog (phase 1 of the Wave 5
 * plan): the wire's `Setting` carries key, value, default, and flags —
 * no description, no grouping, no unit — so the screens hold their own
 * metadata here, structured by the auth-design taxonomy. An item the
 * catalog does not name still renders (the wire is the truth), in the
 * tab reserved for strays, keyed and edited as a plain string.
 */

export type SettingKind = 'boolean' | 'number' | 'enum' | 'duration' | 'text'

export interface SettingMeta {
  key: string
  label: string
  description: string
  kind: SettingKind
  /** The enum's words, in the order the control offers them. */
  choices?: Array<{ value: string; label: string }>
  /** The number control's unit badge and bounds. */
  unit?: string
  min?: number
  max?: number
  /** A text control that edits a list renders taller. */
  multiline?: boolean
  placeholder?: string
}

export interface SettingGroup {
  id: string
  label: string
  description: string
  /** Subsection title → keys, rendered in order. */
  sections: Array<{ title: string; keys: string[] }>
}

const BOOL = (key: string, label: string, description: string): SettingMeta => ({
  key,
  label,
  description,
  kind: 'boolean'
})

export const SETTING_META: Record<string, SettingMeta> = {
  // User & Authentication — email
  'auth.signup_email_enabled': BOOL(
    'auth.signup_email_enabled',
    'Email at sign-up',
    'Whether signing up asks for an email address at all.'
  ),
  'auth.require_email': BOOL(
    'auth.require_email',
    'Require email',
    'Whether an account must carry an email address.'
  ),
  'auth.verify_email_at_signup': BOOL(
    'auth.verify_email_at_signup',
    'Verify email at sign-up',
    'Whether a fresh account must prove its address before it is usable.'
  ),
  'auth.signin_email_enabled': BOOL(
    'auth.signin_email_enabled',
    'Email sign-in',
    'Whether accounts may sign in with their email address.'
  ),
  'auth.signin_email_code_enabled': BOOL(
    'auth.signin_email_code_enabled',
    'Email code sign-in',
    'Whether sign-in offers the emailed one-time code factor.'
  ),
  // User & Authentication — username
  'auth.signup_username_enabled': BOOL(
    'auth.signup_username_enabled',
    'Username at sign-up',
    'Whether signing up may set a username.'
  ),
  'auth.require_username': BOOL(
    'auth.require_username',
    'Require username',
    'Whether an account must carry a username.'
  ),
  // User & Authentication — password (NIST 800-63B)
  'auth.signup_password_enabled': BOOL(
    'auth.signup_password_enabled',
    'Password sign-up',
    'Whether signing up sets a password credential.'
  ),
  'password.min_length': {
    key: 'password.min_length',
    label: 'Minimum length',
    description: 'The shortest password accepted.',
    kind: 'number',
    unit: 'characters',
    min: 1,
    max: 128
  },
  'password.reject_compromised': BOOL(
    'password.reject_compromised',
    'Reject compromised passwords',
    'Whether passwords found in breach corpora are refused.'
  ),
  'password.rule_lowercase': BOOL(
    'password.rule_lowercase',
    'Require lowercase',
    'Whether a password must carry a lowercase letter.'
  ),
  'password.rule_uppercase': BOOL(
    'password.rule_uppercase',
    'Require uppercase',
    'Whether a password must carry an uppercase letter.'
  ),
  'password.rule_number': BOOL(
    'password.rule_number',
    'Require number',
    'Whether a password must carry a digit.'
  ),
  'password.rule_special': BOOL(
    'password.rule_special',
    'Require special character',
    'Whether a password must carry a symbol.'
  ),
  // User & Authentication — passkeys
  'webauthn.allow_synced_passkeys': BOOL(
    'webauthn.allow_synced_passkeys',
    'Allow synced passkeys',
    'Whether passkeys backed by a cloud account (not hardware-bound) may enroll.'
  ),
  'webauthn.user_verification': {
    key: 'webauthn.user_verification',
    label: 'User verification',
    description:
      'How firmly a ceremony must verify the human. Only "required" satisfies an MFA bridge, so a lowered value narrows the step-up ladder.',
    kind: 'enum',
    choices: [
      { value: 'required', label: 'required' },
      { value: 'preferred', label: 'preferred' },
      { value: 'discouraged', label: 'discouraged' }
    ]
  },
  'passkey.max_credentials': {
    key: 'passkey.max_credentials',
    label: 'Passkey cap',
    description: 'How many passkeys one account may hold. Enforcement is at enrollment only.',
    kind: 'number',
    unit: 'passkeys',
    min: 1,
    max: 100
  },
  // User & Authentication — self-service
  'users.self_delete_enabled': BOOL(
    'users.self_delete_enabled',
    'Self-delete',
    'Whether an account may delete itself.'
  ),
  'users.change_email_enabled': BOOL(
    'users.change_email_enabled',
    'Change email',
    'Whether an account may change its own email address.'
  ),
  'users.change_username_enabled': BOOL(
    'users.change_username_enabled',
    'Change username',
    'Whether an account may change its own username.'
  ),
  // Access mode & sign-ups
  'access.mode': {
    key: 'access.mode',
    label: 'Access mode',
    description:
      'Open, anyone may create an account; invite, a valid invitation token is required.',
    kind: 'enum',
    choices: [
      { value: 'open', label: 'open' },
      { value: 'invite', label: 'invite' }
    ]
  },
  'access.allowlist_enabled': BOOL(
    'access.allowlist_enabled',
    'Allowlist on',
    'Whether sign-up accepts only the addresses the allowlist names.'
  ),
  'access.allowlist': {
    key: 'access.allowlist',
    label: 'Allowlist',
    description: 'The accepted addresses or @domains, one per line.',
    kind: 'text',
    multiline: true,
    placeholder: 'ada@example.com\n@trusted.example.com'
  },
  'access.blocklist_enabled': BOOL(
    'access.blocklist_enabled',
    'Blocklist on',
    'Whether the blocklist refuses sign-ups (and sign-ins, when the second flag follows).'
  ),
  'access.blocklist_applies_to_signins': BOOL(
    'access.blocklist_applies_to_signins',
    'Blocklist bites sign-ins',
    'Whether a blocked address is refused at sign-in, not only at sign-up.'
  ),
  'access.block_email_subaddresses': BOOL(
    'access.block_email_subaddresses',
    'Block subaddresses',
    'Whether plus-tagged variants of a registered address are refused at sign-up.'
  ),
  // MFA
  'mfa.required': BOOL(
    'mfa.required',
    'MFA required',
    'Whether every account must enroll a second factor.'
  ),
  'mfa.max_enrollments': {
    key: 'mfa.max_enrollments',
    label: 'Enrollment cap',
    description:
      'How many second factors one account may hold — TOTP devices and passkeys together. Enforcement is at enrollment only.',
    kind: 'number',
    unit: 'factors',
    min: 1,
    max: 50
  },
  // Sessions
  'session.lifetime': {
    key: 'session.lifetime',
    label: 'Session lifetime',
    description:
      'How long a session the caller did not remember lives. Bounds at the reader: 5 minutes to the remembered bound.',
    kind: 'number',
    unit: 'seconds',
    min: 300,
    max: 31536000
  },
  'session.max_lifetime': {
    key: 'session.max_lifetime',
    label: 'Remembered cap',
    description: 'The longest a remembered session may live; the refresh paths never outlive it.',
    kind: 'number',
    unit: 'seconds',
    min: 300,
    max: 315360000
  },
  'session.inactivity_timeout': {
    key: 'session.inactivity_timeout',
    label: 'Inactivity timeout',
    description:
      'How long a session may rest idle before it is refused and rotated out. Bounds at the reader: 5 minutes to 1 year.',
    kind: 'number',
    unit: 'seconds',
    min: 300,
    max: 31536000
  },
  'session.reverification_window': {
    key: 'session.reverification_window',
    label: 'Reverification window',
    description:
      'How long a reauthentication proof counts before a sensitive procedure demands a fresh one.',
    kind: 'number',
    unit: 'seconds',
    min: 60,
    max: 86400
  },
  // Advanced security & protection
  'lockout.enabled': BOOL(
    'lockout.enabled',
    'Lockout on',
    'Whether a failed-password streak at the cap writes a lockout against the account.'
  ),
  'lockout.max_attempts': {
    key: 'lockout.max_attempts',
    label: 'Attempt streak',
    description:
      'The failed-password streak an automated lockout lands at. The reader’s floor is 5.',
    kind: 'number',
    unit: 'attempts',
    min: 5,
    max: 1000
  },
  'lockout.duration': {
    key: 'lockout.duration',
    label: 'Lockout duration',
    description:
      'How long an automated lockout binds. Empty means it never lifts by itself — an administrator unlocks.',
    kind: 'duration',
    placeholder: '1h'
  },
  'auth.user_enumeration_protection': {
    key: 'auth.user_enumeration_protection',
    label: 'Enumeration protection',
    description:
      'Bulk answers plain refusals at a rate limit; strict answers the success shapes that hide whether the account exists.',
    kind: 'enum',
    choices: [
      { value: 'bulk', label: 'bulk' },
      { value: 'strict', label: 'strict' }
    ]
  },
  // Federation & OIDC
  'oidc.end_session_revokes_consent': BOOL(
    'oidc.end_session_revokes_consent',
    'Logout withdraws consent',
    'Off, an RP-initiated logout kills the grants and keeps the ledger; on, the whole consent is withdrawn and the next sign-in asks again.'
  ),
  'oidc.backchannel_logout_enabled': BOOL(
    'oidc.backchannel_logout_enabled',
    'Backchannel logout',
    'Whether a signed logout token is POSTed to every client’s registered backchannel URI when a session ends.'
  ),
  'oidc.refresh_token_hours': {
    key: 'oidc.refresh_token_hours',
    label: 'Refresh token lifetime',
    description: 'How many hours an OIDC refresh token lives. Zero means it never expires.',
    kind: 'number',
    unit: 'hours',
    min: 0,
    max: 87600
  },
  'oidc.offline_refresh_token_hours': {
    key: 'oidc.offline_refresh_token_hours',
    label: 'Offline (persistent) refresh lifetime',
    description:
      'The refresh token lifetime when its grant carries offline_access. Zero means it never expires.',
    kind: 'number',
    unit: 'hours',
    min: 0,
    max: 87600
  },
  'oauth.account_linking_enabled': BOOL(
    'oauth.account_linking_enabled',
    'Account linking',
    'Whether a provider identity may bind to an existing account by verified address.'
  ),
  // Storage (the two catalog items outside the taxonomy's tabs land here)
  'storage.default_bucket': {
    key: 'storage.default_bucket',
    label: 'Default bucket',
    description: 'The bucket the stage path writes into when a feature names none.',
    kind: 'text'
  },
  'storage.signed_url_expires': {
    key: 'storage.signed_url_expires',
    label: 'Signed URL lifetime',
    description: 'How many seconds a presigned link to a stored object stays valid.',
    kind: 'number',
    unit: 'seconds',
    min: 60,
    max: 604800
  }
}

export const SETTING_GROUPS: SettingGroup[] = [
  {
    id: 'authentication',
    label: 'User & Authentication',
    description: 'What signing up and signing in ask for, and the rules the credentials obey.',
    sections: [
      {
        title: 'Email',
        keys: [
          'auth.signup_email_enabled',
          'auth.require_email',
          'auth.verify_email_at_signup',
          'auth.signin_email_enabled',
          'auth.signin_email_code_enabled'
        ]
      },
      {
        title: 'Username',
        keys: ['auth.signup_username_enabled', 'auth.require_username']
      },
      {
        title: 'Password rules',
        keys: [
          'auth.signup_password_enabled',
          'password.min_length',
          'password.reject_compromised',
          'password.rule_lowercase',
          'password.rule_uppercase',
          'password.rule_number',
          'password.rule_special'
        ]
      },
      {
        title: 'Passkeys',
        keys: [
          'webauthn.allow_synced_passkeys',
          'webauthn.user_verification',
          'passkey.max_credentials'
        ]
      },
      {
        title: 'Self-service',
        keys: [
          'users.self_delete_enabled',
          'users.change_email_enabled',
          'users.change_username_enabled'
        ]
      }
    ]
  },
  {
    id: 'access',
    label: 'Access & Sign-ups',
    description: 'Who may create an account, and which addresses are turned away.',
    sections: [
      {
        title: 'Mode',
        keys: ['access.mode', 'access.allowlist_enabled', 'access.allowlist']
      },
      {
        title: 'Blocklist',
        keys: [
          'access.blocklist_enabled',
          'access.blocklist_applies_to_signins',
          'access.block_email_subaddresses'
        ]
      }
    ]
  },
  {
    id: 'mfa',
    label: 'Multi-Factor',
    description: 'The second-factor posture every account is held to.',
    sections: [
      {
        title: 'Policy',
        keys: ['mfa.required', 'mfa.max_enrollments']
      }
    ]
  },
  {
    id: 'sessions',
    label: 'Sessions',
    description: 'How long a session lives, when it idles out, and how long a proof counts.',
    sections: [
      {
        title: 'Windows',
        keys: [
          'session.lifetime',
          'session.max_lifetime',
          'session.inactivity_timeout',
          'session.reverification_window'
        ]
      }
    ]
  },
  {
    id: 'protection',
    label: 'Protection',
    description: 'Lockout and the answers an unknown caller reads.',
    sections: [
      {
        title: 'Lockout',
        keys: ['lockout.enabled', 'lockout.max_attempts', 'lockout.duration']
      },
      {
        title: 'Enumeration',
        keys: ['auth.user_enumeration_protection']
      }
    ]
  },
  {
    id: 'federation',
    label: 'Federation & OIDC',
    description: 'What the provider’s tokens live for and what a logout withdraws.',
    sections: [
      {
        title: 'Tokens & logout',
        keys: [
          'oidc.end_session_revokes_consent',
          'oidc.backchannel_logout_enabled',
          'oidc.refresh_token_hours',
          'oidc.offline_refresh_token_hours',
          'oauth.account_linking_enabled'
        ]
      },
      {
        title: 'Storage',
        keys: ['storage.default_bucket', 'storage.signed_url_expires']
      }
    ]
  }
]

/** The metadata a key the catalog does not name renders with. */
export function metaFor(key: string): SettingMeta {
  return (
    SETTING_META[key] ?? {
      key,
      label: key,
      description: 'A catalog item the console does not document — edit with care.',
      kind: 'text'
    }
  )
}
