// Package sqlitecodec preserves time.Time at the SQL boundary while storing
// timestamps as paired integer Unix seconds and normalized nanoseconds.
// Explicit timestamp projections include the adjacent companion column named _ns.
package sqlitecodec

const DriverName = "memory_sqlite_ns"
