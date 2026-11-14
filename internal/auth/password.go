package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Password length limits, in characters (runes).
const (
	// MinPasswordLength is enforced when an account password is set (`homecore user add|passwd`).
	// It is not enforced at login, so an account created under an older policy can still sign in.
	MinPasswordLength = 12
	// MaxPasswordLength matches maxLength in the OpenAPI LoginRequest; longer input is rejected
	// before any hashing so it cannot be used to burn CPU or memory.
	MaxPasswordLength = 256
)

// Params are the argon2id cost parameters. They are written into every encoded hash, so they can
// be raised later: old hashes keep verifying with their own parameters and are re-hashed with the
// current ones on the next successful login (see Hasher.Verify).
type Params struct {
	MemoryKiB   uint32 // m
	Iterations  uint32 // t
	Parallelism uint8  // p
	SaltLen     uint32
	KeyLen      uint32
}

// DefaultParams targets a Raspberry Pi 4 class device (and Android TV boxes): 32 MiB, 3 passes,
// one lane. That is above the OWASP minimum for argon2id (19 MiB, t=2, p=1), costs roughly a
// quarter to half a second on a Pi 4 and about 50 ms on a desktop, and keeps the peak memory of
// two concurrent logins (see MaxConcurrent) at 64 MiB. Raise it on faster hardware by changing
// this value: stored hashes are upgraded transparently.
var DefaultParams = Params{MemoryKiB: 32 * 1024, Iterations: 3, Parallelism: 1, SaltLen: 16, KeyLen: 32}

// Bounds accepted when parsing a stored hash, so a corrupted or hostile value cannot make the
// server allocate gigabytes.
const (
	maxStoredMemoryKiB   = 256 * 1024
	maxStoredIterations  = 20
	maxStoredParallelism = 16
	maxStoredLen         = 128
)

// ErrPasswordTooLong and ErrPasswordTooShort are returned by ValidateNewPassword.
var (
	ErrPasswordTooLong  = fmt.Errorf("password must be at most %d characters", MaxPasswordLength)
	ErrPasswordTooShort = fmt.Errorf("password must be at least %d characters", MinPasswordLength)
)

// ValidateNewPassword applies the policy for a password being set.
func ValidateNewPassword(pw string) error {
	n := utf8.RuneCountInString(pw)
	switch {
	case n > MaxPasswordLength:
		return ErrPasswordTooLong
	case n < MinPasswordLength:
		return ErrPasswordTooShort
	}
	return nil
}

// ValidateMaxPassword checks only the upper length bound (used for login input).
func ValidateMaxPassword(pw string) error {
	if utf8.RuneCountInString(pw) > MaxPasswordLength {
		return ErrPasswordTooLong
	}
	return nil
}

// Hasher hashes and verifies passwords with argon2id. It limits how many hashes run at once
// because each one allocates Params.MemoryKiB.
type Hasher struct {
	params Params
	sem    chan struct{}
}

// MaxConcurrent is the number of argon2 computations allowed to run at the same time.
const MaxConcurrent = 2

// NewHasher returns a Hasher producing hashes with p.
func NewHasher(p Params) *Hasher {
	return &Hasher{params: p, sem: make(chan struct{}, MaxConcurrent)}
}

// Params returns the parameters new hashes are created with.
func (h *Hasher) Params() Params { return h.params }

func (h *Hasher) acquire(ctx context.Context) (release func(), err error) {
	select {
	case h.sem <- struct{}{}:
		return func() { <-h.sem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Hash returns the encoded argon2id hash of password:
//
//	$argon2id$v=19$m=32768,t=3,p=1$<salt b64>$<key b64>
//
// (standard unpadded base64, the PHC string format).
func (h *Hasher) Hash(ctx context.Context, password string) (string, error) {
	if utf8.RuneCountInString(password) > MaxPasswordLength {
		return "", ErrPasswordTooLong
	}
	salt := make([]byte, h.params.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	release, err := h.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	key := argon2.IDKey([]byte(password), salt, h.params.Iterations, h.params.MemoryKiB, h.params.Parallelism, h.params.KeyLen)
	return encodeHash(h.params, salt, key), nil
}

// Verify reports whether password matches encoded, comparing in constant time. needsRehash is
// true when the hash is valid and the password right but its parameters differ from the current
// ones. A malformed hash or an over-long password is a non-match, not a panic; err is only set
// for a cancelled context.
func (h *Hasher) Verify(ctx context.Context, password, encoded string) (ok, needsRehash bool, err error) {
	p, salt, want, perr := decodeHash(encoded)
	if perr != nil || utf8.RuneCountInString(password) > MaxPasswordLength {
		return false, false, nil
	}
	release, err := h.acquire(ctx)
	if err != nil {
		return false, false, err
	}
	defer release()
	got := argon2.IDKey([]byte(password), salt, p.Iterations, p.MemoryKiB, p.Parallelism, uint32(len(want)))
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return false, false, nil
	}
	return true, p != h.params, nil
}

func encodeHash(p Params, salt, key []byte) string {
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.MemoryKiB, p.Iterations, p.Parallelism, b64.EncodeToString(salt), b64.EncodeToString(key))
}

var errBadHash = errors.New("malformed argon2id hash")

// decodeHash parses an encoded hash. The returned Params carry the salt and key lengths so that
// comparing them with the current Params detects every kind of difference.
func decodeHash(s string) (Params, []byte, []byte, error) {
	parts := strings.Split(s, "$")
	// "", "argon2id", "v=19", "m=..,t=..,p=..", salt, key
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return Params{}, nil, nil, errBadHash
	}
	if v, ok := strings.CutPrefix(parts[2], "v="); !ok || v != strconv.Itoa(argon2.Version) {
		return Params{}, nil, nil, errBadHash
	}
	var p Params
	kv := strings.Split(parts[3], ",")
	if len(kv) != 3 {
		return Params{}, nil, nil, errBadHash
	}
	for i, want := range []string{"m=", "t=", "p="} {
		v, ok := strings.CutPrefix(kv[i], want)
		if !ok {
			return Params{}, nil, nil, errBadHash
		}
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return Params{}, nil, nil, errBadHash
		}
		switch i {
		case 0:
			p.MemoryKiB = uint32(n)
		case 1:
			p.Iterations = uint32(n)
		case 2:
			if n > 255 {
				return Params{}, nil, nil, errBadHash
			}
			p.Parallelism = uint8(n)
		}
	}
	if p.MemoryKiB < 8 || p.MemoryKiB > maxStoredMemoryKiB ||
		p.Iterations < 1 || p.Iterations > maxStoredIterations ||
		p.Parallelism < 1 || p.Parallelism > maxStoredParallelism ||
		uint32(p.Parallelism)*8 > p.MemoryKiB {
		return Params{}, nil, nil, errBadHash
	}
	b64 := base64.RawStdEncoding
	salt, err1 := b64.DecodeString(parts[4])
	key, err2 := b64.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(salt) < 8 || len(salt) > maxStoredLen || len(key) < 16 || len(key) > maxStoredLen {
		return Params{}, nil, nil, errBadHash
	}
	p.SaltLen, p.KeyLen = uint32(len(salt)), uint32(len(key))
	return p, salt, key, nil
}
