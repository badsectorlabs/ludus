package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// The SDK changed an earlier collection-creation migration. Existing Guacamole
// installations already ran that migration, so upgrade their schema separately.
func init() { m.Register(upgradePluginResourceSources, nil) }

func upgradePluginResourceSources(app core.App) error {
	c, err := app.FindCollectionByNameOrId("plugin_resources")
	if err != nil {
		return err
	}
	if c.Fields.GetByName("source") != nil {
		return nil
	}
	return app.RunInTransaction(func(tx core.App) error {
		c.Fields.Add(&core.SelectField{Name: "source", MaxSelect: 1, Values: []string{"uploaded", "server", "licensed"}})
		if err := tx.Save(c); err != nil {
			return err
		}
		records, err := tx.FindAllRecords("plugin_resources")
		if err != nil {
			return err
		}
		for _, r := range records {
			source := "uploaded"
			if r.GetBool("system") {
				source = "server"
				if r.GetString("pluginID") == "ludus-enterprise" {
					source = "licensed"
				}
			}
			r.Set("source", source)
			// Legacy executables speak a different protocol and their old
			// approval did not cover SDK privilege declarations.
			var manifest struct {
				Executable string `json:"executable"`
			}
			if err := r.UnmarshalJSONField("manifest", &manifest); err != nil {
				return err
			}
			if manifest.Executable != "" {
				r.Set("state", "pending")
				r.Set("error", "Replace with an SDK-compatible package and review its privileges before activation")
			}
			if err := tx.Save(r); err != nil {
				return err
			}
		}
		c.Fields.Add(&core.SelectField{Name: "source", Required: true, MaxSelect: 1, Values: []string{"uploaded", "server", "licensed"}})
		c.RemoveIndex("idx_plugin_resources_personal")
		c.RemoveIndex("idx_plugin_resources_global")
		c.RemoveIndex("idx_plugin_resources_system")
		c.AddIndex("idx_plugin_resources_personal", true, "pluginID, owner", "allUsers = FALSE AND source = 'uploaded'")
		c.AddIndex("idx_plugin_resources_global", true, "pluginID", "allUsers = TRUE AND source = 'uploaded'")
		c.AddIndex("idx_plugin_resources_installed", true, "pluginID", "source != 'uploaded'")
		return tx.Save(c)
	})
}
