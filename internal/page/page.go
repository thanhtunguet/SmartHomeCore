// Package page holds the pagination helpers shared by the list endpoints: the `limit` bounds and
// the opaque keyset cursor. A cursor is base64url(JSON) of the sort key of the last item of a page;
// clients must treat it as an opaque string (the format may change).
package page

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
)

// Limit bounds of the `limit` query parameter (api/openapi.yaml, parameter Limit).
const (
	DefaultLimit = 50
	MaxLimit     = 200
)

// ErrBadCursor means the cursor is not one this server issued for this list.
var ErrBadCursor = errors.New("invalid cursor")

// ErrBadLimit means `limit` is outside 1..MaxLimit.
var ErrBadLimit = errors.New("limit must be between 1 and 200")

// Normalize returns the effective limit: DefaultLimit for 0, else the value if in range.
func Normalize(limit int) (int, error) {
	switch {
	case limit == 0:
		return DefaultLimit, nil
	case limit < 1 || limit > MaxLimit:
		return 0, ErrBadLimit
	}
	return limit, nil
}

// Encode renders a cursor value (any JSON-serialisable struct) as an opaque string.
func Encode(v any) string {
	b, err := json.Marshal(v)
	if err != nil { // only plain structs are passed; cannot happen
		panic("page: encode cursor: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// Decode parses a cursor produced by Encode into dst. Unknown fields are rejected so a cursor of
// another list does not decode silently.
func Decode(s string, dst any) error {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return ErrBadCursor
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return ErrBadCursor
	}
	return nil
}
