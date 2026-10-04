package queue

// The queue's tables. They are the engine's own vocabulary — the SQL the
// store composes names them — and the set this package migrates creates
// them (Schema()), so the constant and the DDL travel together.
const (
	// TableQueueTasks is the pending table: the rows the dispatcher claims.
	TableQueueTasks = "public.queue_tasks"
	// TableQueueTasksCompleted is the archive: the settled tasks, the
	// replay stock of which is the dead letters.
	TableQueueTasksCompleted = "public.queue_tasks_completed"

	// TableQueueMigrations is the queue set's version table. It is migrator
	// bookkeeping, never data a dump carries.
	TableQueueMigrations = "public.queue_migrations"
)
