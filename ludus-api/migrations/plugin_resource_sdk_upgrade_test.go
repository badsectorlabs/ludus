package migrations

import (
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"testing"
)

func TestUpgradeLegacyPluginResourcesPreservesSharingAndRequiresSDKReview(t *testing.T) {
	app := pocketbase.NewWithConfig(pocketbase.Config{DefaultDataDir: t.TempDir(), HideStartBanner: true})
	if err := app.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	defer app.ResetBootstrapState()
	c := core.NewBaseCollection("plugin_resources")
	c.Fields.Add(&core.TextField{Name: "pluginID"}, &core.TextField{Name: "owner"}, &core.BoolField{Name: "system"}, &core.BoolField{Name: "allUsers"}, &core.JSONField{Name: "allowedUsers"}, &core.JSONField{Name: "excludedUsers"}, &core.JSONField{Name: "manifest"}, &core.TextField{Name: "state"}, &core.TextField{Name: "error"})
	c.AddIndex("idx_plugin_resources_personal", true, "pluginID, owner", "allUsers = FALSE AND system = FALSE")
	if err := app.Save(c); err != nil {
		t.Fatal(err)
	}
	r := core.NewRecord(c)
	r.Set("pluginID", "legacy-plugin")
	r.Set("owner", "administrator")
	r.Set("allowedUsers", []string{"member"})
	r.Set("excludedUsers", []string{"hidden"})
	r.Set("state", "approved")
	r.Set("manifest", map[string]any{"executable": "plugin", "protocolVersion": 2})
	if err := app.Save(r); err != nil {
		t.Fatal(err)
	}
	if err := upgradePluginResourceSources(app); err != nil {
		t.Fatal(err)
	}
	got, err := app.FindRecordById("plugin_resources", r.Id)
	if err != nil {
		t.Fatal(err)
	}
	if got.GetString("source") != "uploaded" || got.GetString("state") != "pending" || got.GetString("owner") != "administrator" || got.GetStringSlice("allowedUsers")[0] != "member" || got.GetStringSlice("excludedUsers")[0] != "hidden" {
		t.Fatal("legacy resource identity, approval or sharing lost")
	}
	if err := upgradePluginResourceSources(app); err != nil {
		t.Fatal("migration not idempotent", err)
	}
}
