package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The guard helpers wrap a statement without changing it, and quote what they
// are given so a name with an apostrophe cannot break out of the literal.
//
// They are unexported, so this is tested from inside the package.
// pkg/testutils does not import this package, so there is no cycle.
func TestDumpGuards(t *testing.T) {
	guarded := guardedByConstraintCheck("public", "users", "users_pkey",
		`ALTER TABLE "public"."users" ADD CONSTRAINT "users_pkey" PRIMARY KEY (id);`)
	assert.Contains(t, guarded, "pg_constraint")
	assert.Contains(t, guarded, "'users_pkey'")
	assert.Contains(t, guarded, "'users'")
	assert.Contains(t, guarded, "'public'")
	assert.Contains(t, guarded, `ADD CONSTRAINT "users_pkey" PRIMARY KEY (id);`)

	assert.Contains(t, guardedByTypeCheck("public", "jwt_algorithm", "CREATE TYPE x AS ENUM ('a');"),
		"pg_type")

	// A quote in a name is doubled, not left to end the literal early.
	assert.Equal(t, `'it''s'`, quoteLiteral("it's"))

	// pg_indexes never writes the clause, so it is inserted after CREATE INDEX.
	assert.Equal(t,
		"CREATE INDEX IF NOT EXISTS idx_users ON public.users USING btree (id)",
		indexIfNotExists("CREATE INDEX idx_users ON public.users USING btree (id)"))
	assert.Equal(t,
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_users ON public.users USING btree (id)",
		indexIfNotExists("CREATE UNIQUE INDEX idx_users ON public.users USING btree (id)"))
	// Anything else is returned untouched rather than mangled.
	assert.Equal(t, "SELECT 1", indexIfNotExists("SELECT 1"))

	// pg_get_triggerdef never emits OR REPLACE.
	assert.Equal(t, "CREATE OR REPLACE TRIGGER tg BEFORE INSERT ON t",
		triggerOrReplace("CREATE TRIGGER tg BEFORE INSERT ON t"))
	assert.Equal(t, "SELECT 1", triggerOrReplace("SELECT 1"))
}
