package writerapi

import (
	"database/sql"
	"strconv"

	"repotory/internal/db"
)

// CurrentSchemaVersion reads the migrated version stamped in app_config (the
// same key internal/integrity checks). It is published on the endpoint for
// drift logging only, so a missing or unparseable value is reported as 0
// rather than failing startup: the contract works without knowing it.
func CurrentSchemaVersion(database *sql.DB) int {
	raw, err := db.GetConfig(database, "schema_version")
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return n
}
