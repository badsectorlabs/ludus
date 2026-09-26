package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		c, err := app.FindCollectionByNameOrId("plugin_resources")
		if err != nil {
			return err
		}
		c.RemoveIndex("idx_plugin_resources_id")
		c.AddIndex("idx_plugin_resources_personal", true, "pluginID, owner", "allUsers = FALSE AND system = FALSE")
		c.AddIndex("idx_plugin_resources_global", true, "pluginID", "allUsers = TRUE AND system = FALSE")
		c.AddIndex("idx_plugin_resources_system", true, "pluginID", "system = TRUE")
		return app.Save(c)
	}, func(app core.App) error {
		c, err := app.FindCollectionByNameOrId("plugin_resources")
		if err != nil {
			return err
		}
		c.RemoveIndex("idx_plugin_resources_personal")
		c.RemoveIndex("idx_plugin_resources_global")
		c.RemoveIndex("idx_plugin_resources_system")
		// Saving fails safely if scoped copies still exist; never discard them.
		c.AddIndex("idx_plugin_resources_id", true, "pluginID", "")
		return app.Save(c)
	})
}
