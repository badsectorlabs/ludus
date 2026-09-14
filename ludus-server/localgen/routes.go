package localgen

import (
	"fmt"
	"os"
	"strings"
)

func RenderRoutes(rangeNumbers []int) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	for _, n := range rangeNumbers {
		fmt.Fprintf(&b, "# LUDUS MANAGED BLOCK FOR RANGE %d BEGIN\nif [ \"$IFACE\" = \"eth1\" ]; then\n\tip route replace 10.%d.0.0/16 via 192.0.2.%d\nfi\n# LUDUS MANAGED BLOCK FOR RANGE %d END\n", n, n, 100+n, n)
	}
	return b.String()
}

func WriteRoutes(path string, rangeNumbers []int) error {
	return os.WriteFile(path, []byte(RenderRoutes(rangeNumbers)), 0755)
}
