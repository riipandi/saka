package audit

// The recorder's table. It is the engine's own vocabulary — the insert the
// recorder composes names it — and the set this package migrates creates
// it (Schema()), so the constant and the DDL travel together.
const TableAuditLogs = "public.audit_logs"

// The trigger and status values the record's columns accept, and the payload
// keys a delegated record carries. A caller matches these with constants,
// never with string literals.
const (
	// TriggerUser marks an action a caller caused.
	TriggerUser = "user"
	// TriggerSystem marks an action the application itself caused.
	TriggerSystem = "system"

	// StatusSuccess marks a completed action.
	StatusSuccess = "success"
	// StatusFailed marks a refused action.
	StatusFailed = "failed"
)
