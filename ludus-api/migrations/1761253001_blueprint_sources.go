package migrations

import (
	"database/sql"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	m "github.com/pocketbase/pocketbase/migrations"
)

// Adds the sources + source_artifacts collections and extends blueprints
// (source-derived rows live alongside user-created ones, distinguished by a
// populated source field). Template provenance lives in source_artifacts with
// kind="template" so the same template can be claimed by multiple sources.
// Each step is idempotent.
func init() {
	m.Register(func(app core.App) error {
		if err := createSourcesCollection(app); err != nil {
			return err
		}
		if err := createSourceArtifactsCollection(app); err != nil {
			return err
		}
		if err := extendBlueprintsCollection(app); err != nil {
			return err
		}
		if err := migrateBlueprintConfigsToDirs(app); err != nil {
			return err
		}
		return dropBlueprintConfigFileField(app)
	}, func(app core.App) error {
		if err := restoreBlueprintConfigFileField(app); err != nil {
			return err
		}
		if err := stripBlueprintsCollectionExtensions(app); err != nil {
			return err
		}
		for _, name := range []string{"source_artifacts", "sources"} {
			c, err := app.FindCollectionByNameOrId(name)
			if err != nil {
				continue
			}
			if delErr := app.Delete(c); delErr != nil {
				return delErr
			}
		}
		return nil
	})
}

func createSourcesCollection(app core.App) error {
	if existing, err := app.FindCollectionByNameOrId("sources"); err != nil && err != sql.ErrNoRows {
		return err
	} else if existing != nil {
		return nil
	}

	usersCollection, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		return err
	}

	c := core.NewBaseCollection("sources")

	ownerOrAdminRule := types.Pointer(`
	@request.auth.id != "" && (
		@request.auth.isAdmin = true ||
		owner.id = @request.auth.id
	)`)

	c.ListRule = ownerOrAdminRule
	c.ViewRule = ownerOrAdminRule
	c.CreateRule = types.Pointer(`@request.auth.id != ""`)
	c.UpdateRule = ownerOrAdminRule
	c.DeleteRule = ownerOrAdminRule

	c.Fields.Add(
		&core.TextField{Name: "name", Required: true},
		&core.TextField{
			Name:     "sourceID",
			Required: true,
			Pattern:  `^[A-Za-z][A-Za-z0-9_\-]*$`,
		},
		&core.TextField{Name: "description"},
		&core.JSONField{Name: "authors"},
		&core.TextField{Name: "homepage"},
		&core.TextField{Name: "license"},
		&core.SelectField{
			Name:      "type",
			Required:  true,
			MaxSelect: 1,
			Values:    []string{"git", "upload"},
		},
		&core.TextField{Name: "url"},
		&core.TextField{Name: "ref"},
		&core.RelationField{
			Name:          "owner",
			CollectionId:  usersCollection.Id,
			Required:      true,
			MaxSelect:     1,
			CascadeDelete: true,
		},
		&core.DateField{Name: "lastSyncedAt"},
		// lastSyncStatus values: "" (never synced or register-only), "ok",
		// "partial" (sync completed with per-artifact failures recorded in
		// lastSyncError), "error" (clone/walk failed before any install).
		&core.TextField{Name: "lastSyncStatus"},
		&core.TextField{Name: "lastSyncError"},
		&core.AutodateField{Name: "created", OnCreate: true},
		&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
	)

	c.AddIndex("idx_sources_owner_sourceID_unique", true, "owner, sourceID", "")

	return app.Save(c)
}

func createSourceArtifactsCollection(app core.App) error {
	if existing, err := app.FindCollectionByNameOrId("source_artifacts"); err != nil && err != sql.ErrNoRows {
		return err
	} else if existing != nil {
		return nil
	}

	sourcesCollection, err := app.FindCollectionByNameOrId("sources")
	if err != nil {
		return err
	}

	c := core.NewBaseCollection("source_artifacts")
	c.ListRule, c.ViewRule, c.CreateRule, c.UpdateRule, c.DeleteRule = nil, nil, nil, nil, nil

	c.Fields.Add(
		&core.RelationField{
			Name:          "source",
			CollectionId:  sourcesCollection.Id,
			Required:      true,
			MaxSelect:     1,
			CascadeDelete: true,
		},
		&core.SelectField{
			Name:      "kind",
			Required:  true,
			MaxSelect: 1,
			Values:    []string{"template", "local_role", "galaxy_role", "collection", "subscription_role"},
		},
		&core.TextField{Name: "name", Required: true},
		&core.TextField{Name: "version"},
		&core.AutodateField{Name: "created", OnCreate: true},
	)

	c.AddIndex("idx_source_artifacts_unique", true, "source, kind, name", "")

	return app.Save(c)
}

// extendBlueprintsCollection adds the source relation + metadata fields. A
// populated source means source-derived; empty means user-created with
// blueprintDirPath as on-disk home.
func extendBlueprintsCollection(app core.App) error {
	c, err := app.FindCollectionByNameOrId("blueprints")
	if err != nil {
		return err
	}
	sourcesCollection, err := app.FindCollectionByNameOrId("sources")
	if err != nil {
		return err
	}
	add := func(field core.Field) {
		if c.Fields.GetByName(field.GetName()) == nil {
			c.Fields.Add(field)
		}
	}
	add(&core.TextField{Name: "version"})
	add(&core.JSONField{Name: "tags"})
	add(&core.TextField{Name: "min_ludus_version"})
	add(&core.TextField{Name: "lastInstallStatus"})
	add(&core.TextField{Name: "lastInstallError"})
	add(&core.TextField{Name: "lastInstalledAt"})
	add(&core.TextField{Name: "blueprintDirPath"})
	add(&core.RelationField{
		Name:          "source",
		CollectionId:  sourcesCollection.Id,
		Required:      false,
		MaxSelect:     1,
		CascadeDelete: true,
	})
	add(&core.TextField{Name: "sourceBlueprintID"})
	add(&core.TextField{Name: "blueprint_path"})
	add(&core.TextField{Name: "config_path"})
	add(&core.TextField{Name: "requirements_yaml"})

	// blueprintID must be globally unique: a local blueprint id "goad/foo"
	// otherwise collides with source "goad" sub "foo".
	const blueprintIDIndex = "idx_blueprints_blueprintID_unique"
	hasIndex := false
	for _, idx := range c.Indexes {
		if strings.Contains(idx, blueprintIDIndex) {
			hasIndex = true
			break
		}
	}
	if !hasIndex {
		c.AddIndex(blueprintIDIndex, true, "blueprintID", "")
	}

	// Prevents duplicate rows when two sync runs race.
	const sourceUniqueIndex = "idx_blueprints_source_sub_unique"
	hasSrcIndex := false
	for _, idx := range c.Indexes {
		if strings.Contains(idx, sourceUniqueIndex) {
			hasSrcIndex = true
			break
		}
	}
	if !hasSrcIndex {
		c.AddIndex(sourceUniqueIndex, true, "source, sourceBlueprintID", "source != ''")
	}
	return app.Save(c)
}

// migrateBlueprintConfigsToDirs moves any existing FileField config bytes
// to disk-backed dirs. No-op on fresh installs.
func migrateBlueprintConfigsToDirs(app core.App) error {
	blueprintDirRoot := filepath.Join(ludusInstallPathFromEnv(), "blueprints")
	if err := os.MkdirAll(blueprintDirRoot, 0755); err != nil {
		return err
	}

	records, err := app.FindAllRecords("blueprints")
	if err != nil {
		return err
	}

	fsysClient, err := app.NewFilesystem()
	if err != nil {
		return err
	}
	defer fsysClient.Close()

	for _, rec := range records {
		if rec.GetString("blueprintDirPath") != "" {
			continue
		}
		configFileName := rec.GetString("config")
		if configFileName == "" {
			continue
		}

		filePath := path.Join(rec.BaseFilesPath(), configFileName)
		reader, rErr := fsysClient.GetReader(filePath)
		if rErr != nil {
			continue
		}
		data, readErr := io.ReadAll(reader)
		reader.Close()
		if readErr != nil {
			return readErr
		}

		blueprintDir := filepath.Join(blueprintDirRoot, rec.Id)
		if mkErr := os.MkdirAll(blueprintDir, 0755); mkErr != nil {
			return mkErr
		}
		if writeErr := os.WriteFile(filepath.Join(blueprintDir, "range-config.yml"), data, 0644); writeErr != nil {
			return writeErr
		}

		rec.Set("blueprintDirPath", blueprintDir)
		if saveErr := app.Save(rec); saveErr != nil {
			return saveErr
		}
	}
	return nil
}

// dropBlueprintConfigFileField removes the FileField after its bytes have
// been written to disk. The field value is cleared first so Required doesn't
// block the save.
func dropBlueprintConfigFileField(app core.App) error {
	c, err := app.FindCollectionByNameOrId("blueprints")
	if err != nil {
		return err
	}
	field := c.Fields.GetByName("config")
	if field == nil {
		return nil
	}

	if ff, ok := field.(*core.FileField); ok && ff.Required {
		ff.Required = false
		if err := app.Save(c); err != nil {
			return err
		}
	}

	blueprintDirRoot := filepath.Join(ludusInstallPathFromEnv(), "blueprints")
	records, err := app.FindAllRecords("blueprints")
	if err != nil {
		return err
	}
	fsys, err := app.NewFilesystem()
	if err != nil {
		return err
	}
	defer fsys.Close()

	for _, rec := range records {
		configFileName := rec.GetString("config")
		if configFileName == "" {
			continue
		}
		fp := path.Join(rec.BaseFilesPath(), configFileName)
		if r, rErr := fsys.GetReader(fp); rErr == nil {
			data, readErr := io.ReadAll(r)
			r.Close()
			if readErr != nil {
				return readErr
			}
			blueprintDir := rec.GetString("blueprintDirPath")
			if blueprintDir == "" {
				blueprintDir = filepath.Join(blueprintDirRoot, rec.Id)
				if mkErr := os.MkdirAll(blueprintDir, 0755); mkErr != nil {
					return mkErr
				}
				rec.Set("blueprintDirPath", blueprintDir)
			}
			if writeErr := os.WriteFile(filepath.Join(blueprintDir, "range-config.yml"), data, 0644); writeErr != nil {
				return writeErr
			}
		}
		rec.Set("config", "")
		if saveErr := app.Save(rec); saveErr != nil {
			return saveErr
		}
	}

	c2, err := app.FindCollectionByNameOrId("blueprints")
	if err != nil {
		return err
	}
	if c2.Fields.GetByName("config") != nil {
		c2.Fields.RemoveByName("config")
	}
	return app.Save(c2)
}

// restoreBlueprintConfigFileField re-adds the FileField on Down. Bytes are
// not repopulated; the disk blueprint dir is authoritative.
func restoreBlueprintConfigFileField(app core.App) error {
	c, err := app.FindCollectionByNameOrId("blueprints")
	if err != nil {
		return err
	}
	if c.Fields.GetByName("config") != nil {
		return nil
	}
	c.Fields.Add(&core.FileField{
		Name:      "config",
		Required:  false,
		MaxSelect: 1,
		MimeTypes: []string{
			"text/plain",
			"text/yaml",
			"application/yaml",
			"application/x-yaml",
		},
		MaxSize: 1024 * 1024 * 5,
	})
	return app.Save(c)
}

// stripBlueprintsCollectionExtensions reverses extendBlueprintsCollection.
func stripBlueprintsCollectionExtensions(app core.App) error {
	c, err := app.FindCollectionByNameOrId("blueprints")
	if err != nil {
		return err
	}
	for _, name := range []string{
		"version", "tags", "min_ludus_version",
		"lastInstallStatus", "lastInstallError", "lastInstalledAt",
		"blueprintDirPath",
		"source", "sourceBlueprintID", "blueprint_path", "config_path",
		"requirements_yaml",
	} {
		if f := c.Fields.GetByName(name); f != nil {
			_ = f
			c.Fields.RemoveByName(name)
		}
	}
	return app.Save(c)
}

func ludusInstallPathFromEnv() string {
	if p := os.Getenv("LUDUS_INSTALL_PATH"); p != "" {
		return p
	}
	return "/opt/ludus"
}
