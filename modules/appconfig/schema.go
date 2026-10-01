package appconfig

import "time"

// SettingTable is the settings table. The migrations own the schema; this
// constant is how Go code names it, so a table rename touches one line.
const SettingTable = "public.app_settings"

// SettingSchema is one row of SettingTable — one override. The catalog in
// code owns the item's default and flags, so the row carries the key and
// the value alone; whether the value rests sealed is told by the enc:
// prefix of the value and the catalog's flag together. updated_at is set
// on insert and maintained by the trigger on every update after, so a
// row always carries when its override landed.
type SettingSchema struct {
	Key       string     `db:"key"`
	Value     string     `db:"value"`
	CreatedAt time.Time  `db:"created_at"`
	UpdatedAt *time.Time `db:"updated_at"`
}
