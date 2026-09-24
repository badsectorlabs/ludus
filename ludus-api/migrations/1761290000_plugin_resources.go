package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		users, err := app.FindCollectionByNameOrId("users")
		if err != nil {
			return err
		}
		c := core.NewBaseCollection("plugin_resources")
		// All reads and writes go through the authenticated resource API.
		// Never expose package bytes, approval, or access fields via PB rules.
		c.Fields.Add(
			&core.TextField{Name: "pluginID", Required: true, Pattern: `^[a-z][a-z0-9-]{1,63}$`},
			&core.RelationField{Name: "owner", CollectionId: users.Id, MaxSelect: 1},
			&core.BoolField{Name: "allUsers"},
			&core.JSONField{Name: "allowedUsers"},
			&core.JSONField{Name: "excludedUsers"},
			&core.JSONField{Name: "manifest", MaxSize: 65536},
			&core.TextField{Name: "state"},
			&core.TextField{Name: "error"},
			&core.TextField{Name: "sha256"},
			&core.BoolField{Name: "system"},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		c.AddIndex("idx_plugin_resources_personal", true, "pluginID, owner", "allUsers = FALSE AND system = FALSE")
		c.AddIndex("idx_plugin_resources_global", true, "pluginID", "allUsers = TRUE AND system = FALSE")
		c.AddIndex("idx_plugin_resources_system", true, "pluginID", "system = TRUE")
		return app.Save(c)
	}, func(app core.App) error {
		c, err := app.FindCollectionByNameOrId("plugin_resources")
		if err != nil {
			return err
		}
		return app.Delete(c)
	})
}
