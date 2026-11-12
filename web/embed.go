// Package web embeds the static frontend and the offline Swagger UI assets into the binary.
package web

import (
	"embed"
	"io/fs"
)

// static holds the built frontend (`make ui-build`, from the frontend submodule). It is
// git-ignored apart from .gitkeep, which keeps the embed directive valid on a fresh checkout.
//
//go:embed all:static
var staticFS embed.FS

//go:embed placeholder/index.html
var placeholderFS embed.FS

//go:embed swagger/index.html swagger/swagger-initializer.js swagger/swagger-ui.css swagger/swagger-ui-bundle.js
var swaggerFS embed.FS

// Static returns the frontend files (web/static), or a one-page placeholder when the UI has not
// been built into this binary.
func Static() fs.FS {
	ui := mustSub(staticFS, "static")
	if _, err := fs.Stat(ui, "index.html"); err != nil {
		return mustSub(placeholderFS, "placeholder")
	}
	return ui
}

// Swagger returns the Swagger UI files (web/swagger).
func Swagger() fs.FS { return mustSub(swaggerFS, "swagger") }

func mustSub(f fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
