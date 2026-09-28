package appconfig

import "time"

// SettingTable is the settings table. The migrations own the schema; this
// constant is how Go code names it, so a table rename touches one line.
const SettingTable = "public.settings"

// SettingSchema is one row of SettingTable. Whether a value rests sealed is
// told by its enc: prefix alone — there is no flag column — and UpdatedAt
// is nullable by construction: the trigger fills it on the first update,
// and a row that has never been updated carries nothing to say about it.
type SettingSchema struct {
	Key       string     `db:"key"`
	Value     string     `db:"value"`
	Public    bool       `db:"public"`
	CreatedAt time.Time  `db:"created_at"`
	UpdatedAt *time.Time `db:"updated_at"`
}
