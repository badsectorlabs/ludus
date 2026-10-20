package ludusapi

import (
	"os"
	"testing"

	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

// Optionally check the actual separately built Guacamole ZIP against the host
// parser and authenticated upload endpoint, without starting or deploying it.
func TestGuacamoleBuiltSDKPackage(t *testing.T) {
	path := os.Getenv("LUDUS_GUACAMOLE_TEST_PACKAGE")
	if path == "" {
		t.Skip("set LUDUS_GUACAMOLE_TEST_PACKAGE to a built SDK ZIP")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := readPluginPackage(data)
	if err != nil {
		t.Fatal(err)
	}
	m := pkg.manifest
	if m.ID != "ludus-guacamole" || m.Level != protocol.LevelSystem || m.RunAs != protocol.RunAsLudus || !m.ForwardCallerCredentials {
		t.Fatal("unexpected Guacamole SDK privilege declarations")
	}
	env := newPluginTestEnv(t)
	id := env.upload(env.adminToken, "", data)
	r, err := env.pb.FindRecordById("plugin_resources", id)
	if err != nil || r.GetString("state") != "pending" || !sameManifest(resourceManifest(r), m) {
		t.Fatal("real package did not enter SDK approval flow")
	}
}
