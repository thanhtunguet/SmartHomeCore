package tty

import (
	"os"
	"testing"
)

func TestPipesAndFilesAreNotTerminals(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if IsTerminal(r) || IsTerminal(w) {
		t.Fatal("a pipe reported as a terminal")
	}
	f, err := os.Open(os.DevNull)
	if err == nil {
		defer f.Close()
		if IsTerminal(f) {
			t.Fatal("/dev/null reported as a terminal")
		}
	}
	if _, err := ReadPassword(r); err == nil {
		t.Fatal("ReadPassword must fail on a non-terminal instead of echoing")
	}
}
