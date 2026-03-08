package migrations

import (
	"database/sql"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {

		// Check if the collection already exists
		// This allows the migrations to run successfully on a dev machine that
		// previously ran the pre-optimized migrations
		// Should never effect a fresh install or a 1.x to 2.x upgrade
		existingBlueprintsCollection, err := app.FindCollectionByNameOrId("blueprints")
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if existingBlueprintsCollection != nil {
			return nil
		}

		blueprintsCollection := core.NewBaseCollection("blueprints")

		usersCollection, err := app.FindCollectionByNameOrId("users")
		if err != nil {
			return err
		}
		groupsCollection, err := app.FindCollectionByNameOrId("groups")
		if err != nil {
			return err
		}
		hasAccessRule := types.Pointer(`
		@request.auth.id != "" && (
			@request.auth.isAdmin = true ||
			owner.id = @request.auth.id ||
			sharedUsers.id ?= @request.auth.id ||
			(
				@collection.groups.id ?= sharedGroups.id &&
				(
					@collection.groups.members.id ?= @request.auth.id ||
					@collection.groups.managers.id ?= @request.auth.id
				)
			)
		)`)

		ownerOrAdminRule := types.Pointer(`
		@request.auth.id != "" && (
			@request.auth.isAdmin = true ||
			owner.id = @request.auth.id
		)`)

		ownerOrAdminUpdateRule := types.Pointer(`
		@request.auth.id != "" && (
			@request.auth.isAdmin = true ||
			owner.id = @request.auth.id
		) &&
		@request.body.blueprintID:changed = false &&
		@request.body.owner:changed = false &&
		@request.body.sharedUsers:changed = false &&
		@request.body.sharedGroups:changed = false &&
		@request.body.created:changed = false &&
		@request.body.updated:changed = false
		`)

		blueprintsCollection.ListRule = hasAccessRule
		blueprintsCollection.ViewRule = hasAccessRule
		blueprintsCollection.CreateRule = ownerOrAdminRule
		blueprintsCollection.UpdateRule = ownerOrAdminUpdateRule
		blueprintsCollection.DeleteRule = ownerOrAdminRule

		blueprintsCollection.Fields.Add(
			&core.TextField{
				Name:     "blueprintID",
				Required: true,
				Pattern:  `^[A-Za-z][A-Za-z0-9_\-]*(\/[A-Za-z0-9_\-]+){0,2}$`,
			},
			&core.TextField{
				Name:     "name",
				Required: true,
			},
			&core.TextField{
				Name:     "description",
				Required: false,
			},
			&core.RelationField{
				Name:         "owner",
				CollectionId: usersCollection.Id,
				Required:     true,
				MaxSelect:    1,
			},
			&core.FileField{
				Name:      "config",
				Required:  true,
				MaxSelect: 1,
				MimeTypes: []string{
					"text/plain",
					"text/yaml",
					"application/yaml",
					"application/x-yaml",
				},
				MaxSize: 1024 * 1024 * 5, // 5MB
			},
			&core.FileField{
				Name:      "thumbnail",
				Required:  false,
				MaxSelect: 1,
				MimeTypes: []string{
					"image/png",
					"image/jpeg",
					"image/gif",
					"image/webp",
				},
				MaxSize: 1024 * 1024 * 10, // 10MB
			},
			&core.RelationField{
				Name:         "sharedUsers",
				CollectionId: usersCollection.Id,
				Required:     false,
				MaxSelect:    9999,
			},
			&core.RelationField{
				Name:         "sharedGroups",
				CollectionId: groupsCollection.Id,
				Required:     false,
				MaxSelect:    9999,
			},
			&core.AutodateField{
				Name:     "created",
				OnCreate: true,
				OnUpdate: false,
			},
			&core.AutodateField{
				Name:     "updated",
				OnCreate: true,
				OnUpdate: true,
			},
			&core.FileField{
				Name:      "thumbnail",
				Required:  false,
				MaxSelect: 1,
				MimeTypes: []string{
					"image/png",
					"image/jpeg",
					"image/gif",
					"image/webp",
				},
				MaxSize: 1024 * 1024 * 10, // 10MB
			},
		)

		return app.Save(blueprintsCollection)
	}, func(app core.App) error {
		blueprintsCollection, err := app.FindCollectionByNameOrId("blueprints")
		if err != nil {
			return err
		}

		return app.Delete(blueprintsCollection)
	})
}
