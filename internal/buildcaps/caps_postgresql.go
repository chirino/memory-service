//go:build !nopostgresql

package buildcaps

func init() {
	PostgreSQL = true
}
