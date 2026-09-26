package mailer

import "time"

// The templates this binary carries. Each name is the compiled file's base name,
// so a name here and a file in email/templates/ are the same thing.
const (
	TemplateAPIKeyExpiringSoon    = "api-key-expiring-soon"
	TemplateEmailChangeNotice     = "email-change-notice"
	TemplateEmailChangeRequest    = "email-change-request"
	TemplateEmailChangeSuccess    = "email-change-success"
	TemplateEmailVerification     = "email-verification"
	TemplateLoginNewDevice        = "login-with-new-device"
	TemplateOneTimeAccess         = "one-time-access"
	TemplatePasswordReset         = "password-reset"
	TemplatePasswordChangedNotice = "password-changed-notice"
	TemplateTestEmail             = "test-email"
	TemplateUserBanned            = "user-banned"
	TemplateUserUnbanned          = "user-unbanned"
)

// The data each template renders. They are structs rather than maps so the field
// a template names is checked by the compiler at the call site and by the render
// at run time: a template referring to a field the struct lacks fails the render
// instead of printing nothing.
//
// Every date is a string except the one template that formats its own, because
// these values travel a queue as JSON, where a time.Time arrives as a string. The
// struct that carries a time.Time is the exception that proves the rule: it is
// only ever built where the value is still a time.
type (
	// APIKeyExpiringSoonData renders TemplateAPIKeyExpiringSoon.
	APIKeyExpiringSoonData struct {
		Name       string
		APIKeyName string
		ExpiresAt  string
	}
	// EmailChangeNoticeData renders TemplateEmailChangeNotice.
	EmailChangeNoticeData struct {
		Name     string
		OldEmail string
		NewEmail string
	}
	// EmailChangeRequestData renders TemplateEmailChangeRequest.
	EmailChangeRequestData struct {
		Name        string
		OldEmail    string
		NewEmail    string
		ConfirmLink string
	}
	// EmailChangeSuccessData renders TemplateEmailChangeSuccess.
	EmailChangeSuccessData struct {
		Name     string
		NewEmail string
	}
	// EmailVerificationData renders TemplateEmailVerification.
	EmailVerificationData struct {
		UserFullName     string
		VerificationLink string
	}
	// LoginNewDeviceData renders TemplateLoginNewDevice. DateTime is formatted by
	// the template, so it is the one value that stays a time.
	LoginNewDeviceData struct {
		City      string
		Country   string
		IPAddress string
		Device    string
		DateTime  time.Time
	}
	// OneTimeAccessData renders TemplateOneTimeAccess.
	OneTimeAccessData struct {
		Code              string
		LoginLink         string
		LoginLinkWithCode string
		ExpirationString  string
	}
	// PasswordResetData renders TemplatePasswordReset.
	PasswordResetData struct {
		Email     string
		ResetLink string
	}
	// PasswordChangedNoticeData renders TemplatePasswordChangedNotice —
	// the "your password was changed" receipt a completed reset sends.
	PasswordChangedNoticeData struct {
		Name  string
		Email string
	}
	// TestEmailData renders TemplateTestEmail.
	TestEmailData struct {
		Email string
	}
	// UserBannedData renders TemplateUserBanned. ExpiresAt is empty for a
	// ban that never lifts — the template renders its absence as "without
	// an end date" rather than naming no date.
	UserBannedData struct {
		Name      string
		Reason    string
		ExpiresAt string
	}
	// UserUnbannedData renders TemplateUserUnbanned.
	UserUnbannedData struct {
		Name string
	}
)
