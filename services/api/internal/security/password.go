package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id PHC-string hashing (PRD §8.1).
// t=3 with 64 MiB is the RFC 9106 low-memory recommended profile; the old
// t=1 was a single GPU-friendly pass. VerifyPassword parses parameters from
// the stored hash, so legacy t=1 hashes keep verifying without migration.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // 64 MiB
	argonThreads = 4
	argonKeyLen  = 32
)

var ErrInvalidHash = errors.New("invalid password hash")

// Bounds on the parameters read out of a stored hash.
//
// VerifyPassword has always rejected a hash it cannot parse, but it did not
// check the values it had just parsed, and argon2 does not return errors for
// them - it panics, or allocates:
//
//	t = 0      panic("argon2: number of rounds too small")
//	p = 0      panic("argon2: parallelism degree too low")
//	keyLen = 0 nil dereference inside blake2b, via the empty hash segment
//	m = huge   a single make([]block, memory), in the terabytes
//
// All four were reachable from a stored hash, so a single malformed row panicked
// the login path for that account rather than being refused. The limits below
// are generous next to the current defaults (t=3, 64 MiB, p=4, keyLen=32) and
// exist to bound the damage from a corrupted value, not to pin the profile.
const (
	maxArgonMemoryKiB = 1 << 20 // 1 GiB, sixteen times the current default
	maxArgonTime      = 1024
	maxArgonKeyLen    = 1024
)

func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

func VerifyPassword(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, ErrInvalidHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, ErrInvalidHash
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false, ErrInvalidHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, ErrInvalidHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, ErrInvalidHash
	}
	// Everything above checked the SHAPE of the hash. These values came out of
	// it and are about to be handed to argon2, which panics rather than
	// returning an error for several of them - see the constants above.
	if l := len(want); l == 0 || l > maxArgonKeyLen {
		return false, ErrInvalidHash
	}
	if t < 1 || t > maxArgonTime {
		return false, ErrInvalidHash
	}
	if p < 1 {
		return false, ErrInvalidHash
	}
	if m < 8 || m > maxArgonMemoryKiB {
		return false, ErrInvalidHash
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
