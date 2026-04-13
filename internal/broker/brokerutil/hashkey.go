// Package brokerutil provides shared utilities for broker CSV parsers.
package brokerutil

import (
	"crypto/sha256"
	"fmt"
)

// HashKey returns a SHA-256 hex digest of the pipe-joined parts.
// Used to derive deterministic BrokerTxIDs from broker-specific identifiers.
func HashKey(parts ...string) string {
	h := sha256.New()
	for i, p := range parts {
		if i > 0 {
			h.Write([]byte("|"))
		}
		h.Write([]byte(p))
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}
