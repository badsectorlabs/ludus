package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId("templates")
		if err != nil {
			return err
		}

		// The base `icon` file field shipped bare — no size cap, no resize
		// variant. Re-add it configured: a 10MB MIME-restricted cap and a single
		// "fit" thumb so PocketBase can serve a downscaled variant via ?thumb=.
		// ("icon" fits the use — a small square glyph, not a scaled-down photo.)
		collection.Fields.RemoveByName("icon")
		collection.Fields.Add(&core.FileField{
			Name:      "icon",
			Required:  false,
			MaxSelect: 1,
			MimeTypes: []string{"image/png", "image/jpeg", "image/gif", "image/webp"},
			MaxSize:   1024 * 1024 * 10, // 10MB
			// A single high-res, capped "fit" thumb (no crop) that clients
			// downscale as needed: one stored variant stays crisp on hi-DPI
			// displays and gives every consumer one source of truth, so the
			// render sizes can't drift out of sync after a re-upload.
			Thumbs: []string{"256x256f"},
		})

		// Reads are owner-or-admin: an admin lists every template, a user only the
		// ones they own (owner is set from the on-disk dir by the template-sync
		// paths; templates with no owner are admin-only). Updates are admin-only,
		// and the rule locks the sync-managed columns (name/os/owner/shared), so the
		// one change a client can make is replacing the icon — never a rename
		// or re-own.
		collection.ListRule = types.Pointer(`@request.auth.id != "" && (@request.auth.isAdmin = true || owner.id = @request.auth.id)`)
		collection.ViewRule = types.Pointer(`@request.auth.id != "" && (@request.auth.isAdmin = true || owner.id = @request.auth.id)`)
		collection.UpdateRule = types.Pointer(`
		@request.auth.id != "" &&
		@request.auth.isAdmin = true &&
		@request.body.name:changed = false &&
		@request.body.os:changed = false &&
		@request.body.owner:changed = false &&
		@request.body.shared:changed = false
		`)

		return app.Save(collection)
	}, func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId("templates")
		if err != nil {
			return err
		}
		collection.Fields.RemoveByName("icon")
		collection.Fields.Add(&core.FileField{
			Name:      "icon",
			Required:  false,
			MimeTypes: []string{"image/png", "image/jpeg", "image/gif", "image/webp"},
			MaxSize:   1024 * 1024 * 10,
		})
		collection.ListRule = nil
		collection.ViewRule = nil
		collection.UpdateRule = nil
		return app.Save(collection)
	})
}
