package ludusapi

import (
	"strings"
	"testing"
)

func TestAdminEndpointErrorExplainsContainerBoundary(t *testing.T) {
	oldPort := ServerConfiguration.AdminPort
	ServerConfiguration.AdminPort = 9091
	t.Cleanup(func() { ServerConfiguration.AdminPort = oldPort })
	message := adminEndpointError("ludus migrate sdn run")
	for _, expected := range []string{"127.0.0.1:9091 inside the Ludus LXC", "not the Proxmox host", "ludus migrate sdn run", "public Ludus API"} {
		if !strings.Contains(message, expected) {
			t.Fatalf("missing %q in admin endpoint instructions: %s", expected, message)
		}
	}
	if strings.Contains(message, "ssh -L") {
		t.Fatal("instructions must not tunnel to the Proxmox host's loopback admin port")
	}
}
