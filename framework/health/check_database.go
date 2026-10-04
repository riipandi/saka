package health

import (
	"context"
	"fmt"
	"time"
)

// CheckNameDatabase is the name of the database check in a result. It names
// the kind of dependency, never the product behind it, so a report does not
// advertise which database engine a deployment runs.
const CheckNameDatabase = "database"

// DefaultDatabaseTimeout bounds the database probe. It is shorter than the
// checker timeout so a database that hangs still leaves room to report it.
const DefaultDatabaseTimeout = 2 * time.Second

// Pinger is the part of a connection pool a database check needs.
// *datastore.Postgres satisfies it, and so does a fake in a test.
type Pinger interface {
	Ping(ctx context.Context) error
}

// DatabaseCheck reports whether the database pool can reach the database.
//
// Ping uses a connection from the pool, so this also fails while the pool is
// exhausted, which is the state a saturated service is in.
//
// The report carries no target: the REST endpoint publishes this result, and
// which host the process dials is not something an unauthenticated reader
// should learn. The CLI report, which an operator who owns the machine reads,
// uses DatabaseCheckWithTarget instead.
func DatabaseCheck(pool Pinger) Check {
	return databaseCheck(pool, "")
}

// DatabaseCheckWithTarget is DatabaseCheck with the redacted connection
// string in the result's target, for a surface an operator reads directly.
// The endpoint publishes the plain check instead.
func DatabaseCheckWithTarget(pool Pinger, target string) Check {
	return databaseCheck(pool, target)
}

// databaseCheck builds the probe. target is what the report names, empty to
// name nothing.
func databaseCheck(pool Pinger, target string) Check {
	return Check{
		Name:    CheckNameDatabase,
		Target:  target,
		Timeout: DefaultDatabaseTimeout,
		Check: func(ctx context.Context) error {
			if err := pool.Ping(ctx); err != nil {
				return fmt.Errorf("database: %w", err)
			}
			return nil
		},
	}
}
