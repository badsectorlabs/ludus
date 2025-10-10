package ludusapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// MigrateSQLiteToPostgreSQL handles the HTTP request to migrate from SQLite to PocketBase
func MigrateSQLiteToPocketBaseHandler(c *gin.Context) {
	if !isAdmin(c, true) {
		return
	}

	if err := MigrateFromSQLiteToPocketBase(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"result": "Migration from SQLite to PocketBase completed successfully"})
}
