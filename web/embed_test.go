package web

import (
	"io/fs"
	"strings"
	"testing"
)

// Static must always provide an index.html: the built UI when `make ui-build` was run, otherwise
// the placeholder page. Either way the SPA fallback has something to serve.
func TestStaticAlwaysHasIndex(t *testing.T) {
	b, err := fs.ReadFile(Static(), "index.html")
	if err != nil {
		t.Fatalf("Static() has no index.html: %v", err)
	}
	if !strings.Contains(string(b), "HomeCore") {
		t.Fatalf("index.html does not look like HomeCore's UI or placeholder:\n%s", b)
	}
}
