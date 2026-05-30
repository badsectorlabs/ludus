package localgen

import (
	"fmt"
	"os"
	"strings"
)

func RenderRoutes(rangeNumbers []int) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n# Managed by Ludus bootstrap — do not edit\n")
	for _, n := range rangeNumbers {
		fmt.Fprintf(&b, "ip route replace 10.%d.0.0/16 via 192.0.2.%d\n", n, 100+n)
	}
	return b.String()
}

func WriteRoutes(path string, rangeNumbers []int) error {
	return os.WriteFile(path, []byte(RenderRoutes(rangeNumbers)), 0755)
}
