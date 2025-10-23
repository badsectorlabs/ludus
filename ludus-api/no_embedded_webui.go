//go:build !embedwebui

package ludusapi

import "embed"

var embeddedWebUI embed.FS // Empty or default FS
