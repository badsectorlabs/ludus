package localgen

import (
	"os"
	"strings"
	"testing"
)

func TestRenderRoutes(t *testing.T) {
	got := RenderRoutes([]int{2, 5})
	if !strings.Contains(got, "ip route replace 10.2.0.0/16 via 192.0.2.102") {
		t.Fatalf("missing route 2:\n%s", got)
	}
	if !strings.Contains(got, "ip route replace 10.5.0.0/16 via 192.0.2.105") {
		t.Fatalf("missing route 5:\n%s", got)
	}
	if !strings.HasPrefix(got, "#!/bin/sh") {
		t.Fatal("missing shebang")
	}
}

func TestWriteRoutes(t *testing.T) {
	p := t.TempDir() + "/ludus-routes"
	if err := WriteRoutes(p, []int{2}); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0755 {
		t.Fatalf("expected 0755, got %v", st.Mode().Perm())
	}
}
