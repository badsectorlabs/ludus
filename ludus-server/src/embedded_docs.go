//go:build embeddocs

package ludusapi

import "embed"

//go:embed docs
var embeddedDocs embed.FS
