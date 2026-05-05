package ludusapi

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// BundleInputs is what BuildBundle needs from the caller. Absolute paths to
// the user's roles dir and the global packer dir are the caller's job to
// resolve.
type BundleInputs struct {
	BundleRoot      string
	BundleDirName   string // dir name under BundleRoot; falls back to BlueprintID. Set distinct when staging an atomic-swap rebuild.
	BlueprintID     string
	Name            string
	Description     string
	Version         string
	Tags            []string
	MinLudusVersion string
	ConfigBytes     []byte
	RolesPath       string
	PackerDir       string
	SubCatalog      []string
}

// BundleResult reports BuildBundle's outcome. Complete is false when any
// referenced template or role was missing on disk; SkippedTemplates/
// SkippedRoles record those names so the caller can warn the user.
type BundleResult struct {
	Dir              string
	Complete         bool
	SkippedTemplates []string
	SkippedRoles     []string
}

// BuildBundle materialises a self-contained blueprint bundle on disk. On any
// hard failure during materialisation the partial bundle dir is removed so
// the caller observes an atomic outcome. Missing template HCL dirs and
// missing role dirs are NOT hard failures — many Ludus templates are
// user-uploaded VM images with no Packer build config on disk, and roles can
// be referenced by name even when not yet installed.
func BuildBundle(in BundleInputs) (BundleResult, error) {
	res := BundleResult{Complete: true}
	dirName := in.BundleDirName
	if dirName == "" {
		dirName = in.BlueprintID
	}
	bundleDir := filepath.Join(in.BundleRoot, dirName)
	if mkErr := os.MkdirAll(bundleDir, 0755); mkErr != nil {
		return res, fmt.Errorf("create bundle dir: %w", mkErr)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(bundleDir)
		}
	}()

	if writeErr := os.WriteFile(filepath.Join(bundleDir, "range-config.yml"), in.ConfigBytes, 0644); writeErr != nil {
		return res, fmt.Errorf("write range-config.yml: %w", writeErr)
	}

	manifest := BlueprintManifest{
		ManifestVersion: SupportedManifestVersion,
		ID:              in.BlueprintID,
		Name:            in.Name,
		Description:     in.Description,
		Version:         in.Version,
		Tags:            in.Tags,
		MinLudusVersion: in.MinLudusVersion,
		Config:          "range-config.yml",
	}
	manifestBytes, marshalErr := yaml.Marshal(&manifest)
	if marshalErr != nil {
		return res, fmt.Errorf("marshal blueprint.yml: %w", marshalErr)
	}
	if writeErr := os.WriteFile(filepath.Join(bundleDir, "blueprint.yml"), manifestBytes, 0644); writeErr != nil {
		return res, fmt.Errorf("write blueprint.yml: %w", writeErr)
	}

	templateNames, roleNames, parseErr := InferFromRangeConfig(in.ConfigBytes)
	if parseErr != nil {
		return res, fmt.Errorf("parse config.yml: %w", parseErr)
	}

	for _, t := range templateNames {
		src := filepath.Join(in.PackerDir, t)
		if _, statErr := os.Stat(src); statErr != nil {
			res.SkippedTemplates = append(res.SkippedTemplates, t)
			res.Complete = false
			continue
		}
		dst := filepath.Join(bundleDir, "templates", t)
		if cpErr := copyDir(src, dst); cpErr != nil {
			return res, fmt.Errorf("copy template %q: %w", t, cpErr)
		}
	}

	subSet := map[string]bool{}
	for _, n := range in.SubCatalog {
		subSet[n] = true
	}
	var requirementsRoles []RequirementsRole
	var subscriptionRefs []string

	for _, r := range roleNames {
		if subSet[r] {
			subscriptionRefs = append(subscriptionRefs, r)
			continue
		}
		src := filepath.Join(in.RolesPath, r)
		if _, statErr := os.Stat(src); statErr != nil {
			res.SkippedRoles = append(res.SkippedRoles, r)
			res.Complete = false
			continue
		}
		// Galaxy-installable role: pin in requirements.yml; do NOT copy contents.
		if version, ok := readGalaxyInstallVersion(filepath.Join(src, "meta", ".galaxy_install_info")); ok {
			requirementsRoles = append(requirementsRoles, RequirementsRole{Name: r, Version: version})
			continue
		}
		// Truly local role: bundle the bytes.
		dst := filepath.Join(bundleDir, "roles", r)
		if cpErr := copyDir(src, dst); cpErr != nil {
			return res, fmt.Errorf("copy role %q: %w", r, cpErr)
		}
	}

	if len(requirementsRoles) > 0 {
		doc := RequirementsDoc{Roles: requirementsRoles}
		out, marshalErr := yaml.Marshal(&doc)
		if marshalErr != nil {
			return res, fmt.Errorf("marshal requirements.yml: %w", marshalErr)
		}
		if writeErr := os.WriteFile(filepath.Join(bundleDir, "requirements.yml"), out, 0644); writeErr != nil {
			return res, fmt.Errorf("write requirements.yml: %w", writeErr)
		}
	}

	if len(subscriptionRefs) > 0 {
		doc := struct {
			Roles []string `yaml:"roles"`
		}{Roles: subscriptionRefs}
		out, marshalErr := yaml.Marshal(&doc)
		if marshalErr != nil {
			return res, fmt.Errorf("marshal subscription_refs.yml: %w", marshalErr)
		}
		if writeErr := os.WriteFile(filepath.Join(bundleDir, "subscription_refs.yml"), out, 0644); writeErr != nil {
			return res, fmt.Errorf("write subscription_refs.yml: %w", writeErr)
		}
	}

	committed = true
	res.Dir = bundleDir
	return res, nil
}

func readGalaxyInstallVersion(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "version:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "version:")), true
		}
	}
	return "", false
}
