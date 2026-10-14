//go:build cgo

package sqlitecodec

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	gormsqlite "gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestTimestampSQLBoundary(t *testing.T) {
	db, err := sql.Open(DriverName, ":memory:")
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE timestamps(id INTEGER PRIMARY KEY,created_at INTEGER_SECONDS NOT NULL DEFAULT (unixepoch()),created_at_ns INTEGER NOT NULL DEFAULT 0,expires_at INTEGER_SECONDS,expires_at_ns INTEGER)`)
	require.NoError(t, err)
	instants := []time.Time{
		time.Date(1, 1, 1, 0, 0, 0, 123456789, time.UTC), time.Unix(-1, 999999999),
		time.Date(2026, 10, 13, 10, 20, 30, 123456789, time.FixedZone("east", 19800)),
		time.Date(2280, 4, 17, 2, 13, 20, 123456789, time.UTC), time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC),
	}
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			for i, instant := range instants {
				id := i + 1
				if prepared {
					id += len(instants)
				}
				insert := `INSERT INTO timestamps(id,created_at,created_at_ns,expires_at,expires_at_ns) VALUES (?,?,?,?,?)`
				if prepared {
					stmt, e := db.PrepareContext(context.Background(), insert)
					require.NoError(t, e)
					_, e = stmt.Exec(id, instant, &instant)
					require.NoError(t, e)
					require.NoError(t, stmt.Close())
				} else {
					_, e := db.Exec(insert, id, instant, &instant)
					require.NoError(t, e)
				}
				var stored time.Time
				var expires *time.Time
				var sec, ns int64
				var storage string
				query := `SELECT created_at,created_at_ns,expires_at,expires_at_ns,CAST(created_at AS INTEGER) AS raw_seconds,CAST(created_at_ns AS INTEGER) AS raw_nanos,typeof(created_at) FROM timestamps WHERE id=? AND (created_at,created_at_ns)=(?,?)`
				if prepared {
					stmt, e := db.Prepare(query)
					require.NoError(t, e)
					require.NoError(t, stmt.QueryRow(id, instant.UTC()).Scan(&stored, &expires, &sec, &ns, &storage))
					require.NoError(t, stmt.Close())
				} else {
					require.NoError(t, db.QueryRow(query, id, instant.UTC()).Scan(&stored, &expires, &sec, &ns, &storage))
				}
				require.True(t, instant.Equal(stored))
				require.Equal(t, time.UTC, stored.Location())
				require.True(t, instant.Equal(*expires))
				require.Equal(t, instant.Unix(), sec)
				require.Equal(t, int64(instant.Nanosecond()), ns)
				require.Equal(t, "integer", storage)
			}
			query := `SELECT ? + ?`
			if prepared {
				stmt, e := db.Prepare(query)
				require.NoError(t, e)
				defer stmt.Close()
				require.Error(t, stmt.QueryRow(1).Scan(new(int)))
				require.Error(t, stmt.QueryRow(1, 2, 3).Scan(new(int)))
			} else {
				require.Error(t, db.QueryRow(query, 1).Scan(new(int)))
				require.Error(t, db.QueryRow(query, 1, 2, 3).Scan(new(int)))
			}
		})
	}
	// Parameterized statements after the first batch statement are unsupported.
	_, err = db.Exec(`SELECT 1; SELECT ?`, 1)
	require.ErrorContains(t, err, "expects 0 parameters, got 1")
	require.ErrorContains(t, err, "first statement")
	require.ErrorContains(t, db.QueryRow(`SELECT :instant`, sql.Named("instant", instants[0])).Scan(new(int)), "positional parameters")
	var oldest time.Time
	require.NoError(t, db.QueryRow(`SELECT created_at AS oldest_at,created_at_ns AS oldest_at_ns FROM timestamps ORDER BY created_at,created_at_ns LIMIT 1`).Scan(&oldest))
	require.True(t, instants[0].Equal(oldest))

	beforeDefault := time.Now().Truncate(time.Second)
	_, err = db.Exec(`INSERT INTO timestamps(id) VALUES(100)`)
	require.NoError(t, err)
	var defaultTime time.Time
	var nullable *time.Time
	require.NoError(t, db.QueryRow(`SELECT created_at,created_at_ns,expires_at,expires_at_ns FROM timestamps WHERE id=100`).Scan(&defaultTime, &nullable))
	require.False(t, defaultTime.Before(beforeDefault))
	require.False(t, defaultTime.After(time.Now()))
	require.Zero(t, defaultTime.Nanosecond())
	require.Nil(t, nullable)
}

func TestLegacyTimestampPrecision(t *testing.T) {
	for _, input := range []string{"2026-01-02 03:04:05.123456789-07:00", "2026-01-02T15:34:05.123456789+05:30", "2026-01-02T10:04:05.123456789Z"} {
		instant, err := legacyTime(input)
		require.NoError(t, err)
		require.True(t, time.Date(2026, 1, 2, 10, 4, 5, 123456789, time.UTC).Equal(*instant))
	}
	for _, input := range []any{nil, []byte(nil)} {
		result, err := legacyTime(input)
		require.NoError(t, err)
		require.Nil(t, result)
	}
	_, err := legacyTime("invalid")
	require.ErrorContains(t, err, "invalid legacy SQLite timestamp")
}

func TestNullableTimestampSQLBoundary(t *testing.T) {
	instant := time.Date(2280, 4, 17, 2, 13, 20, 123456789, time.FixedZone("east", 19800))
	valid := sql.NullTime{Time: instant, Valid: true}
	invalid := sql.NullTime{Time: instant}
	cases := []struct {
		name  string
		value any
		valid bool
	}{
		{"pointer", &instant, true},
		{"nil pointer", (*time.Time)(nil), false},
		{"valid NullTime", valid, true},
		{"invalid NullTime", invalid, false},
		{"valid NullTime pointer", &valid, true},
		{"invalid NullTime pointer", &invalid, false},
		{"nil NullTime pointer", (*sql.NullTime)(nil), false},
	}
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			db, err := sql.Open(DriverName, ":memory:")
			require.NoError(t, err)
			defer db.Close()
			db.SetMaxOpenConns(1)
			_, err = db.Exec(`CREATE TABLE nullable_timestamp(expires_at INTEGER_SECONDS,expires_at_ns INTEGER); INSERT INTO nullable_timestamp DEFAULT VALUES`)
			require.NoError(t, err)
			update := `UPDATE nullable_timestamp SET expires_at=?,expires_at_ns=?`
			query := `SELECT expires_at,expires_at_ns,typeof(expires_at),typeof(expires_at_ns) FROM nullable_timestamp WHERE (expires_at,expires_at_ns) IS (?,?)`
			exec := func(value any) error { _, err := db.Exec(update, value); return err }
			queryRow := func(value any) *sql.Row { return db.QueryRow(query, value) }
			if prepared {
				stmt, err := db.Prepare(update)
				require.NoError(t, err)
				defer stmt.Close()
				queryStmt, err := db.Prepare(query)
				require.NoError(t, err)
				defer queryStmt.Close()
				exec = func(value any) error { _, err := stmt.Exec(value); return err }
				queryRow = func(value any) *sql.Row { return queryStmt.QueryRow(value) }
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					require.NoError(t, exec(tc.value))
					var got *time.Time
					var secondsType, nanosType string
					require.NoError(t, queryRow(tc.value).Scan(&got, &secondsType, &nanosType))
					if tc.valid {
						require.NotNil(t, got)
						require.True(t, instant.Equal(*got))
						require.Equal(t, "integer", secondsType)
						require.Equal(t, "integer", nanosType)
					} else {
						require.Nil(t, got)
						require.Equal(t, "null", secondsType)
						require.Equal(t, "null", nanosType)
					}
				})
			}
			var isNull bool
			for _, value := range []any{nil, sql.NullString{}, (*string)(nil)} {
				require.NoError(t, db.QueryRow(`SELECT ? IS NULL`, value).Scan(&isNull))
				require.True(t, isNull)
			}
			var number int
			require.NoError(t, db.QueryRow(`SELECT :number`, sql.Named("number", 7)).Scan(&number))
			require.Equal(t, 7, number)
			require.ErrorContains(t, db.QueryRow(`SELECT :instant`, sql.Named("instant", invalid)).Scan(new(time.Time)), "positional parameters")
		})
	}
}

func TestNullableTimestampGORMBoundary(t *testing.T) {
	db, err := gorm.Open(gormsqlite.Dialector{DriverName: DriverName, DSN: ":memory:"}, &gorm.Config{})
	require.NoError(t, err)
	Install(db)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.Exec(`CREATE TABLE nullable_timestamp(id INTEGER PRIMARY KEY,expires_at INTEGER_SECONDS,expires_at_ns INTEGER)`).Error)
	type row struct {
		ID        int
		ExpiresAt *time.Time
	}
	instant := time.Date(2280, 4, 17, 2, 13, 20, 123456789, time.UTC)
	rows := []row{{ID: 1, ExpiresAt: &instant}, {ID: 2}}
	require.NoError(t, db.Table("nullable_timestamp").Create(&rows).Error)
	cases := []struct {
		value any
		valid bool
	}{
		{(*time.Time)(nil), false},
		{sql.NullTime{Time: instant, Valid: true}, true},
		{sql.NullTime{Time: instant}, false},
		{(*sql.NullTime)(nil), false},
		{&instant, true},
	}
	for _, tc := range cases {
		require.NoError(t, db.Table("nullable_timestamp").Where("id=1").Update("expires_at", tc.value).Error)
		var got row
		require.NoError(t, db.Table("nullable_timestamp").Where("id=1").Take(&got).Error)
		if !tc.valid {
			require.Nil(t, got.ExpiresAt)
		} else {
			require.NotNil(t, got.ExpiresAt)
			require.True(t, instant.Equal(*got.ExpiresAt))
		}
	}
	for _, tc := range cases {
		require.NoError(t, db.Exec(`UPDATE nullable_timestamp SET expires_at=?,expires_at_ns=? WHERE id=1`, tc.value).Error)
		var got *time.Time
		require.NoError(t, sqlDB.QueryRow(`SELECT expires_at,expires_at_ns FROM nullable_timestamp WHERE id=1`).Scan(&got))
		if !tc.valid {
			require.Nil(t, got)
		} else {
			require.NotNil(t, got)
			require.True(t, instant.Equal(*got))
		}
	}
}
