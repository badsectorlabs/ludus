package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		// init a new auth collection with the default system fields and auth options
		rangesCollection := core.NewBaseCollection("ranges")

		// Allow access if the user is authenticated and is either directly assigned the range,
		// or is a member/manager of a group that is assigned the range. Or if they are an Admin.
		hasAccessRule := types.Pointer(`
		@request.auth.id != "" && (
			@request.auth.isAdmin = true ||
			@request.auth.ranges.id ?= id ||
			(
				@collection.groups.ranges.id ?= id &&
				(
					@collection.groups.members.id ?= @request.auth.id ||
					@collection.groups.managers.id ?= @request.auth.id
				)
			)
		)`)

		// Allow access if the user is authenticated and directly assigned the range
		// or a manager of a group that is assigned the range. Admins always have access.
		// We also check explicitly that the only fields that are allowed to be changed are: name, description, purpose and thumbnail
		// This is to prevent users from changing other fields that are not allowed to be changed directly via the API
		isManagerOrDirectlyAssignedRule := types.Pointer(`
		@request.auth.id != "" && (
			@request.auth.isAdmin = true ||
			@request.auth.ranges.id ?= id ||
			(
				@collection.groups.ranges.id ?= id &&
				@collection.groups.managers.id ?= @request.auth.id
			)
		) &&
		@request.body.rangeNumber:changed = false &&
		@request.body.rangeID:changed = false &&
		@request.body.lastDeployment:changed = false &&
		@request.body.numberOfVMs:changed = false &&
		@request.body.testingEnabled:changed = false &&
		@request.body.allowedDomains:changed = false &&
		@request.body.allowedIPs:changed = false &&
		@request.body.rangeState:changed = false &&
		@request.body.created:changed = false &&
		@request.body.updated:changed = false
		`)

		rangesCollection.ListRule = hasAccessRule
		rangesCollection.ViewRule = hasAccessRule
		rangesCollection.UpdateRule = isManagerOrDirectlyAssignedRule

		rangesCollection.Fields.Add(
			&core.NumberField{
				Name:     "rangeNumber",
				Required: true,
				OnlyInt:  true,
			},
			&core.TextField{
				Name:     "rangeID",
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
			&core.TextField{
				Name:     "purpose",
				Required: false,
			},
			&core.DateField{
				Name:     "lastDeployment",
				Required: false,
			},
			&core.NumberField{
				Name:     "numberOfVMs",
				Required: false,
				OnlyInt:  true,
			},
			&core.BoolField{
				Name:     "testingEnabled",
				Required: false,
			},
			&core.JSONField{
				Name:     "allowedDomains",
				Required: false,
			},
			&core.JSONField{
				Name:     "allowedIPs",
				Required: false,
			},
			&core.TextField{
				Name:     "rangeState",
				Required: false,
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
				MimeTypes: []string{"image/png", "image/jpeg", "image/gif", "image/webp"},
				MaxSize:   1024 * 1024 * 10, // 10MB
			},
		)

		return app.Save(rangesCollection)
	}, func(app core.App) error { // optional revert operation
		rangesCollection, err := app.FindCollectionByNameOrId("ranges")
		if err != nil {
			return err
		}

		return app.Delete(rangesCollection)
	})
}
