//go:build !nosqlite && windows

package sqlite

import (
	"context"
	"fmt"
)

func initSQLiteVectorExtension() {}

func serializeSQLiteVector(_ []float32) ([]byte, error) {
	return nil, fmt.Errorf("sqlite vector support is unavailable on windows")
}

func detectVecSupport(_ context.Context, _ sqliteExecContexter) (bool, error) {
	return false, nil
}
