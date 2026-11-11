// Package web embeds the static frontend and the offline Swagger UI assets into the binary.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:static
var staticFS embed.FS

//go:embed swagger/index.html swagger/swagger-initializer.js swagger/swagger-ui.css swagger/swagger-ui-bundle.js
var swaggerFS embed.FS

// Static returns the frontend files (web/static).
func Static() fs.FS { return mustSub(staticFS, "static") }

// Swagger returns the Swagger UI files (web/swagger).
func Swagger() fs.FS { return mustSub(swaggerFS, "swagger") }

func mustSub(f fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
