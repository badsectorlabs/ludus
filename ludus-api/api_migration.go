package ludusapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// MigrateSQLiteToPostgreSQL handles the HTTP request to migrate from SQLite to PostgreSQL
func MigrateSQLiteToPostgreSQL(c *gin.Context) {
	if !isAdmin(c, true) {
		return
	}

	if err := MigrateFromSQLite(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"result": "Migration from SQLite to PostgreSQL completed successfully"})
}
