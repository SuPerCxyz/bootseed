// Package web 内嵌 bootseed-server 门户前端静态资源.
package web

import "embed"

// Files 内嵌门户前端与辅助脚本。
//
//go:embed index.html login.html app.js style.css bootseed-enter.sh
var Files embed.FS
