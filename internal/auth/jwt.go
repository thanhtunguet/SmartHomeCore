package auth

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SigningKeySize is the length of the HS256 key in bytes.
const SigningKeySize = 32

// ClockLeeway is how far in the future a token's iat may lie before the token is rejected. The
// access token is verified by the same host that issued it, so there is no cross-host skew; the
// leeway only absorbs small backwards clock steps (NTP corrections on a device without a battery
// backed clock). It is NOT applied to exp: an expired token is expired at the second it says,
// and a client simply refreshes. A larger backwards step invalidates tokens issued before it,
// which also only costs a refresh.
const ClockLeeway = time.Minute

// Claims are the JWT claims of an access token.
type Claims struct {
	Subject  string `json:"sub"` // user id
	Username string `json:"username"`
	IssuedAt int64  `json:"iat"` // unix seconds
	Expires  int64  `json:"exp"` // unix seconds
	ID       string `json:"jti"`
}

// UserLookup resolves the user an access token claims to belong to. JWT calls it on every
// request, so a disabled or deleted account loses access immediately instead of when the token
// expires (decision recorded in ADR-013).
type UserLookup interface {
	UserByID(ctx context.Context, id string) (User, error)
}

// JWT issues and verifies HS256 access tokens and implements Authenticator.
//
// Verification is strict: the header must be exactly alg=HS256 (typ JWT or absent, no other
// members such as crit, kid, jku or embedded keys), so `none`, RS256/HS384 confusion and
// downgrade tricks are rejected before the signature is looked at; the signature is compared in
// constant time before the payload is parsed.
type JWT struct {
	key   []byte
	ttl   time.Duration
	users UserLookup
	now   func() time.Time
}

// NewJWT creates a JWT authenticator. key must be SigningKeySize random bytes. now may be nil.
func NewJWT(key []byte, ttl time.Duration, users UserLookup, now func() time.Time) (*JWT, error) {
	if len(key) != SigningKeySize {
		return nil, fmt.Errorf("jwt: signing key must be %d bytes", SigningKeySize)
	}
	if ttl <= 0 {
		return nil, errors.New("jwt: ttl must be positive")
	}
	if now == nil {
		now = time.Now
	}
	return &JWT{key: bytes.Clone(key), ttl: ttl, users: users, now: now}, nil
}

// TTL is the lifetime of issued access tokens.
func (j *JWT) TTL() time.Duration { return j.ttl }

var b64url = base64.RawURLEncoding

// jwtHeader is the only header HomeCore emits and accepts.
var jwtHeader = b64url.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))

// Issue creates an access token for u valid for TTL(). It returns the token and its expiry.
func (j *JWT) Issue(u User) (token string, expires time.Time, err error) {
	now := j.now()
	expires = now.Add(j.ttl)
	jti := make([]byte, 16)
	if _, err := rand.Read(jti); err != nil {
		return "", time.Time{}, err
	}
	payload, err := json.Marshal(Claims{
		Subject: u.ID, Username: u.Username,
		IssuedAt: now.Unix(), Expires: expires.Unix(), ID: b64url.EncodeToString(jti),
	})
	if err != nil {
		return "", time.Time{}, err
	}
	signingInput := jwtHeader + "." + b64url.EncodeToString(payload)
	return signingInput + "." + b64url.EncodeToString(j.sign(signingInput)), expires, nil
}

func (j *JWT) sign(signingInput string) []byte {
	m := hmac.New(sha256.New, j.key)
	m.Write([]byte(signingInput))
	return m.Sum(nil)
}

// Parse verifies the signature, the algorithm pinning and the time claims of token and returns
// its claims. It does not consult the user table; use Authenticate for that.
func (j *JWT) Parse(token string) (Claims, error) {
	h, rest, ok := strings.Cut(token, ".")
	if !ok {
		return Claims{}, ErrUnauthenticated
	}
	p, s, ok := strings.Cut(rest, ".")
	if !ok || strings.Contains(s, ".") {
		return Claims{}, ErrUnauthenticated
	}

	rawHeader, err := b64url.DecodeString(h)
	if err != nil {
		return Claims{}, ErrUnauthenticated
	}
	var hdr map[string]any
	if err := json.Unmarshal(rawHeader, &hdr); err != nil {
		return Claims{}, ErrUnauthenticated
	}
	if alg, _ := hdr["alg"].(string); alg != "HS256" {
		return Claims{}, ErrUnauthenticated
	}
	for k, v := range hdr {
		switch k {
		case "alg":
		case "typ":
			if t, _ := v.(string); !strings.EqualFold(t, "JWT") {
				return Claims{}, ErrUnauthenticated
			}
		default:
			return Claims{}, ErrUnauthenticated
		}
	}

	sig, err := b64url.DecodeString(s)
	if err != nil || !hmac.Equal(sig, j.sign(h+"."+p)) {
		return Claims{}, ErrUnauthenticated
	}

	rawClaims, err := b64url.DecodeString(p)
	if err != nil {
		return Claims{}, ErrUnauthenticated
	}
	var c Claims
	if err := json.Unmarshal(rawClaims, &c); err != nil || c.Subject == "" || c.Expires == 0 || c.ID == "" {
		return Claims{}, ErrUnauthenticated
	}
	now := j.now()
	if now.Unix() >= c.Expires || time.Unix(c.IssuedAt, 0).After(now.Add(ClockLeeway)) {
		return Claims{}, ErrUnauthenticated
	}
	return c, nil
}

// Authenticate implements Authenticator: a valid token for an existing, enabled user.
func (j *JWT) Authenticate(ctx context.Context, bearerToken string) (Principal, error) {
	c, err := j.Parse(bearerToken)
	if err != nil {
		return Principal{}, err
	}
	u, err := j.users.UserByID(ctx, c.Subject)
	if err != nil || u.Disabled {
		return Principal{}, ErrUnauthenticated
	}
	return Principal{UserID: u.ID, Username: u.Username}, nil
}
