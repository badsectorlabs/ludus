package ludusapi

import (
	"fmt"
	"time"

	"github.com/pocketbase/dbx"
	_ "modernc.org/sqlite"
)

const sqliteBusyTimeout = 10 * time.Second

// connectLudusSQLite applies the SQLite settings used by every host and plugin
// PocketBase connection. The busy timeout is encoded in the DSN so the driver
// applies it to every physical connection opened by database/sql.
func connectLudusSQLite(dbPath string) (*dbx.DB, error) {
	pragmas := fmt.Sprintf(
		"?_pragma=busy_timeout(%d)&_pragma=journal_mode(WAL)&_pragma=journal_size_limit(200000000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_pragma=temp_store(MEMORY)&_pragma=cache_size(-32000)",
		sqliteBusyTimeout.Milliseconds(),
	)
	return dbx.Open("sqlite", dbPath+pragmas)
}
