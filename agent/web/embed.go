// Package web 内嵌 bootseed-agent 的前端静态资源.
package web

import "embed"

// Files 是内嵌的前端文件集合(index.html / app.js / style.css).
//
//go:embed index.html app.js style.css
var Files embed.FS
