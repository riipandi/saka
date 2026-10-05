package scheduler

// The scheduler's state table. It is the engine's own vocabulary — the SQL
// the claim composes names it — and the set this package migrates creates
// it (Schema()), so the constant and the DDL travel together.
const TableSchedulerJobs = "public.scheduler_jobs"

// TableSchedulerMigrations is the scheduler set's version table. It is
// migrator bookkeeping, never data a dump carries.
const TableSchedulerMigrations = "public.scheduler_migrations"
