package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		// An internal SQL table deliberately has no PocketBase API endpoint.
		// Passwords use the same authenticated encryption as users.proxmoxPassword.
		_, err := app.DB().NewQuery(`CREATE TABLE _ludus_password_rotations (
			user_id TEXT PRIMARY KEY NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			endpoint TEXT NOT NULL,
			proxmox_user TEXT NOT NULL,
			old_password TEXT NOT NULL,
			new_password TEXT NOT NULL,
			submitted INTEGER NOT NULL DEFAULT 0
		)`).Execute()
		return err
	}, func(app core.App) error {
		_, err := app.DB().NewQuery("DROP TABLE _ludus_password_rotations").Execute()
		return err
	})
}
