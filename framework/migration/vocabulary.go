package migration

// AppVersionTable is the version table the application's own set records
// into. It lives beside the toolkit so a composed run and a reader of the
// schema agree on the name without an import cycle through internal/.
const AppVersionTable = "app_migration"
