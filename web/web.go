// Package web holds the embedded browser UI served at "/".
package web

import "embed"

// Files contains the static assets (index.html) compiled into the binary,
// so the server ships as a single file with no external asset directory.
//
//go:embed index.html
var Files embed.FS
