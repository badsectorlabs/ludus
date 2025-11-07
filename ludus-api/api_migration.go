package ludusapi

import (
	"net/http"

	"github.com/pocketbase/pocketbase/core"
)

// MigrateSQLiteToPostgreSQL handles the HTTP request to migrate from SQLite to PocketBase
func MigrateSQLiteToPocketBaseHandler(e *core.RequestEvent) error {
	if !e.Auth.GetBool("isAdmin") {
		return JSONError(e, http.StatusForbidden, "You are not an admin and cannot migrate from SQLite to PocketBase")
	}

	if err := MigrateFromSQLiteToPocketBase(); err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}

	return JSONResult(e, http.StatusOK, "Migration from SQLite to PocketBase completed successfully")
}
