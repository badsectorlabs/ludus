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
			// uploaded: approved by an administrator; server: placed by root;
			// licensed: installed by the license flow.
			&core.SelectField{Name: "source", Required: true, MaxSelect: 1, Values: []string{"uploaded", "server", "licensed"}},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		// Uploads are unique per owner (personal) or globally (all users);
		// installed packages are unique by plugin ID.
		c.AddIndex("idx_plugin_resources_personal", true, "pluginID, owner", "allUsers = FALSE AND source = 'uploaded'")
		c.AddIndex("idx_plugin_resources_global", true, "pluginID", "allUsers = TRUE AND source = 'uploaded'")
		c.AddIndex("idx_plugin_resources_installed", true, "pluginID", "source != 'uploaded'")
		return app.Save(c)
	}, func(app core.App) error {
		c, err := app.FindCollectionByNameOrId("plugin_resources")
		if err != nil {
			return err
		}
		return app.Delete(c)
	})
}
