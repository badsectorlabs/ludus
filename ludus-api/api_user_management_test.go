package ludusapi

import "testing"

func TestUserRealmDefault(t *testing.T) {
	cfg := Configuration{}
	_ = cfg.ApplyShimAndValidate() // will error on missing endpoints, ignore
	if cfg.ProxmoxUserRealm != "pve" {
		t.Fatalf("expected default realm pve, got %q", cfg.ProxmoxUserRealm)
	}
}
