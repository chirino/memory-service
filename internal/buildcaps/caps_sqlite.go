//go:build !nosqlite

package buildcaps

func init() {
	SQLite = true
}
