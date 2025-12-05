package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		rangesCollection, err := app.FindCollectionByNameOrId("ranges")
		if err != nil {
			return err
		}

		// Allow access if the user is authenticated and is either directly assigned the range,
		// or is a member/manager of a group that is assigned the range. Or if they are an Admin.
		hasAccessRule := types.Pointer(`
		@request.auth.id != "" && (
			@request.auth.isAdmin = true || 
			@request.auth.ranges ?= id || 
			(
				@collection.groups.ranges ?= id && 
				(
					@collection.groups.members ?= @request.auth.id || 
					@collection.groups.managers ?= @request.auth.id
				)
			)
		)`)

		// Allow access if the user is authenticated and directly assigned the range
		// or a manager of a group that is assigned the range. Admins always have access.
		// We also check explicitly that the only fields that are allowed to be changed are: name, description, and purpose
		// This is to prevent users from changing other fields that are not allowed to be changed directly via the API
		isManagerOrDirectlyAssignedRule := types.Pointer(`
		@request.auth.id != "" && (
			@request.auth.isAdmin = true || 
			@request.auth.ranges ?= id || 
			(
				@collection.groups.ranges ?= id && 
				@collection.groups.managers ?= @request.auth.id
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
		rangesCollection.DeleteRule = nil // Use the ludus handler for these operations
		rangesCollection.CreateRule = nil

		return app.Save(rangesCollection)

	}, nil)
}
