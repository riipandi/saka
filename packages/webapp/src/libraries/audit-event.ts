/** The event vocabulary the wire carries, read as sentences; unknown events stay legible. */
export function formatEvent(event: string): string {
  const KNOWN: Record<string, string> = {
    sign_in: 'Signed in',
    account_created: 'Account created',
    account_updated: 'Account updated',
    account_deleted: 'Account deleted',
    email_verification_sent: 'Verification email sent',
    email_verified: 'Email verified',
    profile_picture_updated: 'Profile picture updated',
    profile_picture_reset: 'Profile picture reset'
  }
  return KNOWN[event] ?? event.replaceAll('_', ' ')
}
