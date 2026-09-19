package clickhouse

import (
	"crypto/sha256"
	"encoding/hex"
)

func eventID(exporterID, cursor, kind, action, change string) string {
	h := sha256.New()
	for _, value := range []string{exporterID, cursor, kind, action, change} {
		h.Write([]byte(value))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}
