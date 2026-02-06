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

		// Use relation-id lookups explicitly so auth relation checks work
		// consistently when listing by filter before update.
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

		return app.Save(rangesCollection)
	}, nil)
}

