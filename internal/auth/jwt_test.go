package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)} }
func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type mapUsers map[string]User

func (m mapUsers) UserByID(_ context.Context, id string) (User, error) {
	if u, ok := m[id]; ok {
		return u, nil
	}
	return User{}, ErrUserNotFound
}

func testKey(b byte) []byte {
	k := make([]byte, SigningKeySize)
	for i := range k {
		k[i] = b
	}
	return k
}

func newTestJWT(t *testing.T, users UserLookup, clk *fakeClock) *JWT {
	t.Helper()
	j, err := NewJWT(testKey(1), 15*time.Minute, users, clk.Now)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

// forge signs an arbitrary header and payload with key, to build hostile tokens.
func forge(key []byte, header, payload string) string {
	in := b64url.EncodeToString([]byte(header)) + "." + b64url.EncodeToString([]byte(payload))
	m := hmac.New(sha256.New, key)
	m.Write([]byte(in))
	return in + "." + b64url.EncodeToString(m.Sum(nil))
}

func TestJWTIssueAndAuthenticate(t *testing.T) {
	clk := newFakeClock()
	u := User{ID: "u1", Username: "alice"}
	j := newTestJWT(t, mapUsers{"u1": u}, clk)

	tok, exp, err := j.Issue(u)
	if err != nil {
		t.Fatal(err)
	}
	if want := clk.Now().Add(15 * time.Minute); !exp.Equal(want) {
		t.Fatalf("expiry %v, want %v", exp, want)
	}
	c, err := j.Parse(tok)
	if err != nil {
		t.Fatal(err)
	}
	if c.Subject != "u1" || c.Username != "alice" || c.IssuedAt != clk.Now().Unix() || c.Expires != exp.Unix() || c.ID == "" {
		t.Fatalf("unexpected claims %+v", c)
	}
	p, err := j.Authenticate(context.Background(), tok)
	if err != nil || p != (Principal{UserID: "u1", Username: "alice"}) {
		t.Fatalf("Authenticate: %+v %v", p, err)
	}
	tok2, _, _ := j.Issue(u)
	c2, _ := j.Parse(tok2)
	if c2.ID == c.ID {
		t.Fatal("jti must be unique per token")
	}
	if hdr, _ := b64url.DecodeString(strings.Split(tok, ".")[0]); string(hdr) != `{"alg":"HS256","typ":"JWT"}` {
		t.Fatalf("unexpected header %s", hdr)
	}
}

func TestJWTExpiry(t *testing.T) {
	clk := newFakeClock()
	u := User{ID: "u1", Username: "alice"}
	j := newTestJWT(t, mapUsers{"u1": u}, clk)
	tok, _, _ := j.Issue(u)

	clk.Advance(15*time.Minute - time.Second)
	if _, err := j.Authenticate(context.Background(), tok); err != nil {
		t.Fatalf("one second before expiry: %v", err)
	}
	clk.Advance(time.Second)
	if _, err := j.Authenticate(context.Background(), tok); err != ErrUnauthenticated {
		t.Fatalf("at expiry: %v, want ErrUnauthenticated", err)
	}
}

func TestJWTClockLeewayOnIssuedAt(t *testing.T) {
	clk := newFakeClock()
	u := User{ID: "u1", Username: "alice"}
	j := newTestJWT(t, mapUsers{"u1": u}, clk)

	tok, _, _ := j.Issue(u)
	clk.Advance(-30 * time.Second) // small backwards clock step: still fine
	if _, err := j.Parse(tok); err != nil {
		t.Fatalf("iat 30s in the future must be tolerated: %v", err)
	}
	clk.Advance(-2 * time.Minute) // iat now 2.5 minutes ahead of the clock
	if _, err := j.Parse(tok); err == nil {
		t.Fatal("iat far in the future must be rejected")
	}
}

func TestJWTRejectsTamperingAndWrongKey(t *testing.T) {
	clk := newFakeClock()
	u := User{ID: "u1", Username: "alice"}
	users := mapUsers{"u1": u, "u2": {ID: "u2", Username: "mallory"}}
	j := newTestJWT(t, users, clk)
	tok, _, _ := j.Issue(u)
	parts := strings.Split(tok, ".")

	// Payload swapped for another subject, original signature kept.
	evil := b64url.EncodeToString([]byte(`{"sub":"u2","username":"mallory","iat":` + itoa(clk.Now().Unix()) +
		`,"exp":` + itoa(clk.Now().Add(time.Hour).Unix()) + `,"jti":"x"}`))
	flipped := []byte(parts[2])
	flipped[0] ^= 1

	other, _ := NewJWT(testKey(2), 15*time.Minute, users, clk.Now)
	otherTok, _, _ := other.Issue(u)

	cases := map[string]string{
		"payload tampered":    parts[0] + "." + evil + "." + parts[2],
		"signature flipped":   parts[0] + "." + parts[1] + "." + string(flipped),
		"signature dropped":   parts[0] + "." + parts[1] + ".",
		"signed by wrong key": otherTok,
		"two segments":        parts[0] + "." + parts[1],
		"four segments":       tok + ".x",
		"empty":               "",
		"garbage":             "not a jwt",
	}
	for name, bad := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := j.Authenticate(context.Background(), bad); err != ErrUnauthenticated {
				t.Fatalf("got %v, want ErrUnauthenticated", err)
			}
		})
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestJWTStrictAlgorithmPinning(t *testing.T) {
	clk := newFakeClock()
	users := mapUsers{"u1": {ID: "u1", Username: "alice"}}
	j := newTestJWT(t, users, clk)
	key := testKey(1) // the attacker even knows the key: only the header is wrong
	payload := `{"sub":"u1","username":"alice","iat":` + itoa(clk.Now().Unix()) + `,"exp":` + itoa(clk.Now().Add(time.Hour).Unix()) + `,"jti":"x"}`

	// Sanity: the helper produces an acceptable token with the right header.
	if _, err := j.Parse(forge(key, `{"alg":"HS256","typ":"JWT"}`, payload)); err != nil {
		t.Fatalf("control token rejected: %v", err)
	}

	for name, header := range map[string]string{
		"none":            `{"alg":"none","typ":"JWT"}`,
		"None":            `{"alg":"None","typ":"JWT"}`,
		"empty alg":       `{"alg":"","typ":"JWT"}`,
		"missing alg":     `{"typ":"JWT"}`,
		"HS384":           `{"alg":"HS384","typ":"JWT"}`,
		"HS512":           `{"alg":"HS512","typ":"JWT"}`,
		"RS256":           `{"alg":"RS256","typ":"JWT"}`,
		"lowercase hs256": `{"alg":"hs256","typ":"JWT"}`,
		"unknown member":  `{"alg":"HS256","typ":"JWT","kid":"1"}`,
		"crit":            `{"alg":"HS256","typ":"JWT","crit":["exp"]}`,
		"wrong typ":       `{"alg":"HS256","typ":"JWE"}`,
		"alg not string":  `{"alg":["HS256"],"typ":"JWT"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := j.Parse(forge(key, header, payload)); err == nil {
				t.Fatalf("header %s accepted", header)
			}
		})
	}
	// Classic alg=none with an empty signature.
	none := b64url.EncodeToString([]byte(`{"alg":"none"}`)) + "." + b64url.EncodeToString([]byte(payload)) + "."
	if _, err := j.Parse(none); err == nil {
		t.Fatal("unsigned alg=none token accepted")
	}
	// typ may be absent.
	if _, err := j.Parse(forge(key, `{"alg":"HS256"}`, payload)); err != nil {
		t.Fatalf("header without typ rejected: %v", err)
	}
}

func TestJWTRejectsIncompleteClaims(t *testing.T) {
	clk := newFakeClock()
	j := newTestJWT(t, mapUsers{"u1": {ID: "u1"}}, clk)
	h := `{"alg":"HS256","typ":"JWT"}`
	exp := itoa(clk.Now().Add(time.Hour).Unix())
	for name, payload := range map[string]string{
		"no sub":     `{"exp":` + exp + `,"jti":"x"}`,
		"no exp":     `{"sub":"u1","jti":"x"}`,
		"no jti":     `{"sub":"u1","exp":` + exp + `}`,
		"string exp": `{"sub":"u1","exp":"` + exp + `","jti":"x"}`,
		"not json":   `nope`,
	} {
		if _, err := j.Parse(forge(testKey(1), h, payload)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestJWTDisabledOrDeletedUserIsRejected(t *testing.T) {
	clk := newFakeClock()
	users := mapUsers{"u1": {ID: "u1", Username: "alice"}}
	j := newTestJWT(t, users, clk)
	tok, _, _ := j.Issue(users["u1"])

	disabled := users["u1"]
	disabled.Disabled = true
	users["u1"] = disabled
	if _, err := j.Authenticate(context.Background(), tok); err != ErrUnauthenticated {
		t.Fatalf("disabled user: %v", err)
	}
	delete(users, "u1")
	if _, err := j.Authenticate(context.Background(), tok); err != ErrUnauthenticated {
		t.Fatalf("deleted user: %v", err)
	}
}

func TestNewJWTValidatesInput(t *testing.T) {
	if _, err := NewJWT(make([]byte, 16), time.Minute, nil, nil); err == nil {
		t.Error("short key accepted")
	}
	if _, err := NewJWT(testKey(1), 0, nil, nil); err == nil {
		t.Error("zero ttl accepted")
	}
}
