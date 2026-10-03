package password

import (
	"uuid"
)

// UserPasswordSchema is one row of entity.TableUserPasswords. It lists only the columns
// the application writes, so a migration can add a column with a default
// without touching this struct. The db tags are the column names the query
// builder uses.
type UserPasswordSchema struct {
	UserID       uuid.UUID `db:"user_id"`
	PasswordHash string    `db:"password_hash"`
}
