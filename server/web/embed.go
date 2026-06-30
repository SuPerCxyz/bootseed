// Package web 内嵌 bootseed-server 门户前端静态资源。
package web

import "embed"

// Files 内嵌门户前端（index.html / app.js / style.css）。
//
//go:embed index.html app.js style.css
var Files embed.FS
