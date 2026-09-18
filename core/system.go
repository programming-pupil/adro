package core

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"time"
)

// SystemClock is the production wall-clock dependency. Reducers still receive
// it through an explicit interface rather than reading the system clock.
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

// CryptoIDs creates opaque production identifiers while preserving the
// injectable IDGenerator contract used by deterministic tests.
type CryptoIDs struct {
	fallback atomic.Uint64
}

func (g *CryptoIDs) NewID(namespace string) string {
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err == nil {
		return namespace + "-" + hex.EncodeToString(entropy[:])
	}
	seed := fmt.Sprintf("%s:%d:%d", namespace, time.Now().UnixNano(), g.fallback.Add(1))
	digest := sha256.Sum256([]byte(seed))
	return namespace + "-" + hex.EncodeToString(digest[:16])
}
