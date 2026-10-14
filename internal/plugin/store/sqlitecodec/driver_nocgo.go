//go:build !cgo

package sqlitecodec

import (
	"database/sql"
	sqlite3 "github.com/mattn/go-sqlite3"
)

// Keep the driver name available to builds without SQLite support. The upstream
// stub reports that opening SQLite requires CGO.
func init() { sql.Register(DriverName, &sqlite3.SQLiteDriver{}) }
