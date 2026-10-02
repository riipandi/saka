# Authentication Design

Keep this document as simple as possible!

## User & authentication

### Email

- [x] **Sign-up with email**: Allow users to sign up with their email address (default: true)
- [x] **Require email address**: Users must provide an email address to sign up and must maintain one on their account at all times (default: true)
- [x] **Verify at sign-up**: Require users to verify their email addresses before they can sign-up (default: true)
  - Email verification code: Verify by entering a one-time passcode sent to the email address
- [x] **Sign-in with email**: Allow users to sign in with their email address (default: true)
  - Email verification code: Users can sign-in with an email verification code

### Username

- [x] **Sign-up with username**: Allow users to add a username during sign-up (default: false)
- [x] **Require username**: Users must create a username for their account (default: false)
- [x] **Sign-in with username**: Allow users to sign in with their username (default: false)

### Password

- [x] **Sign-up with password**: Require users to sign up with a password (default: true)
- [x] **Add password to account**: Allow users to add a password to their account (default: true) - related to Device Trust

#### Password requirements

- Minimum password length: Passwords must contain N or more characters (default: 8 characters)
- Reject compromised passwords: Passwords that are compromised will be rejected upon sign-up, sign-in, and password changes. Powered by HaveIBeenPwned (default: true)
- Enforce minimum password strength: Options: weak|normal|strong. Powered by zxcvbn. (default: true/normal)
- Password rules: default None
  - Require at least 1 lowercase character (default: false)
  - Require at least 1 uppercase character (default: false)
  - Require at least 1 number (default: false)
  - Require at least 1 special character (default: false)

### Passkeys & Biometrics

- [x] **Sign-in with passkey**: Allow users to sign in with a passkey (default: false)
- [x] **Add passkey to account**: Allow users to add a passkey to their account (default: false)
- [ ] **Sign-in with mobile biometrics**: Allow users to sign in with Face ID, Touch ID, or device biometrics (default: false)
  - Note: Only applies to iOS and Android apps using the Native API

### User Model

- [x] **First and last name**: Users have the ability to set their first and last name (default: true)
- [x] **Require first and last name**: Users must provide both first and last name at sign-up (default: true)
- [x] **Allow users to delete their account**: Can override on a per-user basis in the user profile. (default: false)
- [x] **Allow users to change their email address** (default: true)
- [x] **Allow users to change their username** (default: true)

### Phone (deferred)

- [ ] **Sign-up with phone**: Allow users to sign up with a phone number (default: false)
- [ ] **Sign-in with phone**: Allow users to sign in with a phone number (default: false)

---

## SSO Connections

- [ ] **Account linking**: Allow users to link their account with an external SSO provider (default: true)

---

## Multi-Factor

- [x] **Require multi-factor authentication**: Will enforce users to setup multi-factor authentication after sign-in and sign-up. (default: false)

### MFA strategies

- [ ] **SMS verification code**: Send the user a one-time verification code via SMS (default: false)
- [ ] **WhatsApp verification code**: Send the user a one-time verification code via WhatsApp (default: false)
- [x] **Authenticator application**: Allow users to add an authenticator application to retrieve a TOTP code from a service such as Google Authenticator (default: false)
- [x] **Backup codes**: Generate a list of unique codes a user can save and use once (default: false)

> Authenticator application must be enabled to generate backup codes

---

## Access Mode (default: open)

- [x] **Open**: Sign-ups are enabled and all users can join your application.
  - Allowlist: Only allow sign-ups from accounts with pre-approved identifiers (default: false)
- [x] **Invite-only**: Sign-ups are disabled, and only users who are invited can join (signup token).
- [ ] **Waitlist**: Sign-ups are disabled, but people can join a waitlist. (dropped)

---

## Sessions

### Session lifetime

- [x] **Maximum lifetime**: Set the maximum lifetime duration for a session. Accepts between 5 minutes and 10 years. (default: 7 days)
- [x] **Inactivity timeout**: Set the inactivity timeout for a session. Accepts between 5 minutes and 1 year. (default: 6 hours)
- [x] **Reverification window**: Customize how long a successful sign-in or reverification remains valid for protected sensitive actions. (default: 30 minutes)

> You should be aware of browser limitations that may cause users to be signed out before the configured maximum lifetime, even when this feature is disabled.

### Multi-session handling (deferred)

- [ ] Allow users to be signed into more than one account at a time, users may switch the active account. (default: false)

---

## Advanced Features

### User & Authentication Rules (deferred)

- [ ] **Lockout policy**: Configure how many login attempts are allowed before an account is locked. (default: enabled)
  - Maximum attempt limit: The number of consecutive failed login attempts before protection is activated. (default: 100)
  - Lockout duration: (indefinite|timelimit) The amount of time a user is locked out from their account after 100 failed attempts. (default: timelimit 1 hour)
- [ ] **Device Trust**: Helps protect against credential stuffing by treating new devices as untrusted for password sign-ins. (default: false)
- [ ] **Bot sign-up protection**: New sign-ups will include a browser verification step powered by Cloudflare Turnstile. (default: false)
- [ ] **User enumeration protection**: Prevent attackers from determining if even a single email address or phone number has an account. (default: bulk)
  - Bulk user enumeration protection: Less private, more common - rate limits prevent determining which email addresses and phone numbers are registered in bulk, but targeted attacks are still feasible.
  - Strict user enumeration protection: More private - logical changes prevent attackers from determining if even a single email address or phone number has an account.

### Email Rules (deferred)

- [ ] **Block email subaddresses**: Prevent multiple accounts from signing up with one email address. Once an email is registered, subaddressed variants like `<foo+tag@example.com>`, `<a.lice@gmail.com>`, and `<bob-tag@yahoo.com>` are blocked from signing up or being added to an existing account.
- [ ] **Block sign-ups that use disposable email addresses**: If enabled, all sign-up attempts using an email address from a disposable email domain will be rejected.

### Blocklist (deferred)

- [ ] **Blocklist**: Block specific account identifiers from signing up. (default: false)
- [ ] **Apply allowlist and blocklist to sign-ins**: When enabled, the allowlist and blocklist will also apply to user sign-ins, not just sign-ups. (default: false)

> Must enable the blocklist or the allowlist to use this setting.

## Customization (deferred)

### Email templates

#### Authentication

- [ ] **Invitation**: Send an invitation email to new users to join your application.
- [ ] **Verification code**: Send a verification code for authentication or account confirmation.

> **Notice**: No magic links and no verification links — ever. Every email-based flow
> (sign-up verification, sign-in, email change, invitation) delivers a **single-use
> code** only. The former "Email link - Sign in / Sign up / Verify email" items are
> removed from this design; do not plan, implement, or re-add link-based channels.

#### Security

- [ ] **Account Locked**: Send a notification to your users whenever their account is locked due to multiple login attempts.
- [ ] **Password changed**: Confirm to users that their password has been successfully changed.
- [ ] **Password removed**: Inform users that their password has been removed from the account.
- [ ] **Primary email address changed**: Notify users when their primary email address has been updated.
- [ ] **Reset password code**: Send a reset password code to users to reset their password.
- [ ] **Sign in from new client**: Alert users when a sign-in occurs from a new or unrecognized device.
- [ ] **Sign in attempt with non-existent email**: Send a notification to your users when they try to sign into an account that doesn't exist. (requires strict enumeration protection)
- [ ] **Sign up attempt with existing email**: Send a notification to your users when there is a sign up attempt matching their email. (requires strict enumeration protection)

#### Passkeys

- [ ] **Passkey added**: Notify users when a new passkey is added to their account.
- [ ] **Passkey removed**: Inform users when a passkey has been removed from their account.

#### Waitlist (dropped)

- [ ] **Waitlist confirmation**: Confirm a user's successful addition to your application's waitlist.
- [ ] **Waitlist invitation**: Invite users from your waitlist to join your application.
