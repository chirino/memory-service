//go:build !nosqlite && !windows

package sqlite

import (
	"context"
	"strings"

	sqlitevec "github.com/asg017/sqlite-vec-go-bindings/cgo"
)

func initSQLiteVectorExtension() {
	sqlitevec.Auto()
}

func serializeSQLiteVector(embedding []float32) ([]byte, error) {
	return sqlitevec.SerializeFloat32(embedding)
}

func detectVecSupport(ctx context.Context, db sqliteExecContexter) (bool, error) {
	const probeTable = "__memory_service_vec_probe"
	probeVector, err := serializeSQLiteVector([]float32{0})
	if err != nil {
		return false, err
	}
	if _, err := db.ExecContext(ctx, "CREATE TEMP TABLE IF NOT EXISTS "+probeTable+" (score REAL)"); err != nil {
		return false, err
	}
	defer func() {
		_, _ = db.ExecContext(ctx, "DROP TABLE IF EXISTS temp."+probeTable)
	}()
	if _, err := db.ExecContext(ctx, "DELETE FROM temp."+probeTable); err != nil {
		return false, err
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO temp."+probeTable+"(score) VALUES (vec_distance_cosine(?, ?))", probeVector, probeVector); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such function: vec_distance_cosine") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
