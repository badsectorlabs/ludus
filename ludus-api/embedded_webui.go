//go:build embedwebui

package ludusapi

import "embed"

// We must use "all:" prefix to embed the _next directory. From the docs:
// "If a pattern begins with the prefix ‘all:’, then the rule for walking directories is changed to include those files beginning with ‘.’ or ‘_’."
// https://pkg.go.dev/embed

//go:embed all:webUI
var embeddedWebUI embed.FS
