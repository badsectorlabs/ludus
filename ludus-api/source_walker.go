package ludusapi

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// maxBlueprintDepth caps how deep blueprints/ can nest before WalkSourceRepo
// stops descending. blueprintManifestIDRegex permits up to 2 slashes in an id
// (3 segments), so a leaf may live at blueprints/<a>/<b>/<c>/.
const maxBlueprintDepth = 2

type WalkedSource struct {
	Source     *SourceManifest   // nil if source.yml absent
	Blueprints []WalkedBlueprint // sorted by Manifest.ID
	Templates  []string          // <repo>/templates/<name>/ absolute paths
	LocalRoles []string          // <repo>/roles/<name>/ absolute paths
}

type WalkedBlueprint struct {
	Dir              string
	Manifest         *BlueprintManifest
	ConfigPath       string
	RequirementsYAML []byte // nil if no requirements.yml
	ThumbnailPath    string
}

// WalkSourceRepo scans a source repo's on-disk checkout. Tolerates missing
// optional files; only fails on malformed required files.
func WalkSourceRepo(rootDir string) (*WalkedSource, error) {
	w := &WalkedSource{}

	if data, err := os.ReadFile(filepath.Join(rootDir, "source.yml")); err == nil {
		s, perr := ParseSourceManifest(data)
		if perr != nil {
			return nil, fmt.Errorf("source.yml: %w", perr)
		}
		w.Source = s
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	bpRoot := filepath.Join(rootDir, "blueprints")
	bps, err := walkBlueprintTree(bpRoot, bpRoot, 0)
	if err != nil {
		return nil, err
	}
	w.Blueprints = bps

	w.Templates = walkSubdirs(filepath.Join(rootDir, "templates"))
	w.LocalRoles = walkSubdirs(filepath.Join(rootDir, "roles"))

	sort.Slice(w.Blueprints, func(i, j int) bool {
		return w.Blueprints[i].Manifest.ID < w.Blueprints[j].Manifest.ID
	})
	return w, nil
}

// walkBlueprintTree finds every directory under blueprints/ that contains a
// blueprint.yml. Validates that the manifest id matches the directory path —
// blueprints/windows/dc/blueprint.yml must have id: windows/dc.
func walkBlueprintTree(bpRoot, dir string, depth int) ([]WalkedBlueprint, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []WalkedBlueprint
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		// Skip symlinks: a `blueprints/sneaky -> /etc` could escape the
		// checkout, and real source repos never need symlinks here.
		if info, infoErr := e.Info(); infoErr == nil && info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		sub := filepath.Join(dir, e.Name())
		hasManifest := false
		if _, err := os.Stat(filepath.Join(sub, "blueprint.yml")); err == nil {
			hasManifest = true
		}
		if hasManifest {
			bp, perr := walkBlueprint(sub)
			rel, _ := filepath.Rel(bpRoot, sub)
			relSlash := filepath.ToSlash(rel)
			if perr != nil {
				return nil, fmt.Errorf("blueprints/%s: %w", relSlash, perr)
			}
			if bp == nil {
				continue
			}
			if bp.Manifest.ID != relSlash {
				return nil, fmt.Errorf("blueprints/%s: manifest id %q does not match directory path", relSlash, bp.Manifest.ID)
			}
			out = append(out, *bp)
			continue
		}
		if depth >= maxBlueprintDepth {
			continue
		}
		deeper, err := walkBlueprintTree(bpRoot, sub, depth+1)
		if err != nil {
			return nil, err
		}
		out = append(out, deeper...)
	}
	return out, nil
}

func walkBlueprint(bpDir string) (*WalkedBlueprint, error) {
	manifestPath := filepath.Join(bpDir, "blueprint.yml")
	data, err := os.ReadFile(manifestPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	m, err := ParseBlueprintManifest(data)
	if err != nil {
		return nil, fmt.Errorf("blueprint.yml: %w", err)
	}
	bp := &WalkedBlueprint{
		Dir:        bpDir,
		Manifest:   m,
		ConfigPath: filepath.Join(bpDir, m.Config),
	}
	if _, err := os.Stat(bp.ConfigPath); err != nil {
		return nil, fmt.Errorf("config file %s missing: %w", m.Config, err)
	}
	if data, err := os.ReadFile(filepath.Join(bpDir, "requirements.yml")); err == nil {
		bp.RequirementsYAML = data
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if m.ThumbnailPath != "" {
		thumbPath := filepath.Join(bpDir, m.ThumbnailPath)
		if _, err := os.Stat(thumbPath); err == nil {
			bp.ThumbnailPath = thumbPath
		}
	}
	return bp, nil
}

func WalkBlueprintDir(blueprintDir string) (*WalkedBlueprint, error) {
	bp, err := walkBlueprint(blueprintDir)
	if err != nil {
		return nil, err
	}
	if bp == nil {
		return nil, fmt.Errorf("no blueprint.yml found in %s", blueprintDir)
	}
	return bp, nil
}

func walkSubdirs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out
}
