package page

import (
	"errors"
	"testing"
)

func TestNormalize(t *testing.T) {
	for in, want := range map[int]int{0: DefaultLimit, 1: 1, 200: 200} {
		if got, err := Normalize(in); err != nil || got != want {
			t.Errorf("Normalize(%d) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []int{-1, 201} {
		if _, err := Normalize(in); !errors.Is(err, ErrBadLimit) {
			t.Errorf("Normalize(%d): %v, want ErrBadLimit", in, err)
		}
	}
}

func TestCursorRoundTripAndRejection(t *testing.T) {
	type cur struct {
		N string `json:"n"`
		I string `json:"i"`
	}
	var got cur
	if err := Decode(Encode(cur{"pc", "id1"}), &got); err != nil || got != (cur{"pc", "id1"}) {
		t.Fatalf("round trip: %+v, %v", got, err)
	}
	type other struct {
		T int64 `json:"t"`
	}
	for name, s := range map[string]string{
		"not base64":      "!!!",
		"not json":        "bm9wZQ",
		"other list":      Encode(other{1}),
		"trailing fields": Encode(map[string]string{"n": "a", "i": "b", "x": "c"}),
	} {
		if err := Decode(s, &got); !errors.Is(err, ErrBadCursor) {
			t.Errorf("%s: %v, want ErrBadCursor", name, err)
		}
	}
}
