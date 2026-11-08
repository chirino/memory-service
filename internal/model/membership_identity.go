package model

import (
	"encoding/base64"
	"github.com/google/uuid"
)

// MembershipResourceID encodes the fixed-width group UUID and exact user identity.
// This is a reversible identity encoding, not an anonymization function.
func MembershipResourceID(group uuid.UUID, user string) string {
	return base64.RawURLEncoding.EncodeToString(append(group[:16:16], []byte(user)...))
}
