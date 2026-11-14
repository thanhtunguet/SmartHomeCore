package auth

import (
	"context"
	"strings"
	"testing"
	"time"
)

var fastParams = Params{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32}

func TestHashVerifyRoundTrip(t *testing.T) {
	ctx := context.Background()
	h := NewHasher(fastParams)
	enc, err := h.Hash(ctx, "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, "$argon2id$v=19$m=64,t=1,p=1$") {
		t.Fatalf("encoded hash lacks parameters: %q", enc)
	}
	if strings.Contains(enc, "correct") {
		t.Fatal("hash leaks the password")
	}
	ok, rehash, err := h.Verify(ctx, "correct horse battery", enc)
	if err != nil || !ok || rehash {
		t.Fatalf("right password: ok=%v rehash=%v err=%v", ok, rehash, err)
	}
	if ok, _, _ := h.Verify(ctx, "correct horse batterY", enc); ok {
		t.Fatal("wrong password accepted")
	}
	enc2, _ := h.Hash(ctx, "correct horse battery")
	if enc == enc2 {
		t.Fatal("two hashes of one password are identical: salt not random")
	}
}

func TestVerifyFlagsParameterUpgrade(t *testing.T) {
	ctx := context.Background()
	old := NewHasher(fastParams)
	enc, _ := old.Hash(ctx, "a long enough password")

	stronger := fastParams
	stronger.Iterations = 2
	ok, rehash, err := NewHasher(stronger).Verify(ctx, "a long enough password", enc)
	if err != nil || !ok || !rehash {
		t.Fatalf("old-parameter hash must verify and ask for a rehash: ok=%v rehash=%v err=%v", ok, rehash, err)
	}
	// A wrong password never reports rehash.
	if ok, rehash, _ := NewHasher(stronger).Verify(ctx, "nope nope nope nope", enc); ok || rehash {
		t.Fatalf("wrong password: ok=%v rehash=%v", ok, rehash)
	}
}

func TestVerifyRejectsMalformedHashes(t *testing.T) {
	ctx := context.Background()
	h := NewHasher(fastParams)
	good, _ := h.Hash(ctx, "a long enough password")
	parts := strings.Split(good, "$")

	bad := map[string]string{
		"empty":            "",
		"plaintext":        "a long enough password",
		"wrong algorithm":  strings.Replace(good, "argon2id", "argon2i", 1),
		"wrong version":    strings.Replace(good, "v=19", "v=16", 1),
		"missing field":    "$argon2id$v=19$m=64,t=1,p=1$" + parts[4],
		"bad base64":       "$argon2id$v=19$m=64,t=1,p=1$!!!$!!!",
		"zero iterations":  "$argon2id$v=19$m=64,t=0,p=1$" + parts[4] + "$" + parts[5],
		"huge memory":      "$argon2id$v=19$m=4194304,t=1,p=1$" + parts[4] + "$" + parts[5],
		"huge iterations":  "$argon2id$v=19$m=64,t=4000000000,p=1$" + parts[4] + "$" + parts[5],
		"zero parallelism": "$argon2id$v=19$m=64,t=1,p=0$" + parts[4] + "$" + parts[5],
		"extra parts":      good + "$x",
		"short key":        "$argon2id$v=19$m=64,t=1,p=1$" + parts[4] + "$AAAA",
	}
	for name, enc := range bad {
		t.Run(name, func(t *testing.T) {
			ok, rehash, err := h.Verify(ctx, "a long enough password", enc)
			if ok || rehash || err != nil {
				t.Fatalf("malformed hash: ok=%v rehash=%v err=%v", ok, rehash, err)
			}
		})
	}
}

func TestPasswordLengthLimits(t *testing.T) {
	ctx := context.Background()
	h := NewHasher(fastParams)
	long := strings.Repeat("a", MaxPasswordLength+1)
	if _, err := h.Hash(ctx, long); err != ErrPasswordTooLong {
		t.Fatalf("Hash over-long: %v", err)
	}
	exact := strings.Repeat("ß", MaxPasswordLength) // counted in characters, not bytes
	enc, err := h.Hash(ctx, exact)
	if err != nil {
		t.Fatalf("256 characters must be accepted: %v", err)
	}
	if ok, _, _ := h.Verify(ctx, exact, enc); !ok {
		t.Fatal("256-character password does not verify")
	}
	if ok, _, _ := h.Verify(ctx, long, enc); ok {
		t.Fatal("over-long password verified")
	}
	for pw, want := range map[string]error{
		"short":                  ErrPasswordTooShort,
		strings.Repeat("a", 11):  ErrPasswordTooShort,
		strings.Repeat("a", 12):  nil,
		strings.Repeat("é", 12):  nil, // 12 characters, 24 bytes
		strings.Repeat("a", 256): nil,
		strings.Repeat("a", 257): ErrPasswordTooLong,
	} {
		if got := ValidateNewPassword(pw); got != want {
			t.Errorf("ValidateNewPassword(len %d) = %v, want %v", len(pw), got, want)
		}
	}
}

func TestHashHonoursCancelledContext(t *testing.T) {
	h := NewHasher(fastParams)
	for range MaxConcurrent { // occupy every slot
		h.sem <- struct{}{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := h.Hash(ctx, "a long enough password"); err == nil {
		t.Fatal("Hash must give up when no slot frees before the context ends")
	}
}

func TestDefaultParamsAreSane(t *testing.T) {
	p := DefaultParams
	if p.MemoryKiB < 19*1024 || p.Iterations < 2 || p.Parallelism < 1 || p.SaltLen < 16 || p.KeyLen < 32 {
		t.Fatalf("default argon2id parameters below the documented minimum: %+v", p)
	}
	if _, _, _, err := decodeHash(encodeHash(p, make([]byte, p.SaltLen), make([]byte, p.KeyLen))); err != nil {
		t.Fatalf("default parameters are rejected by the parser bounds: %v", err)
	}
}
