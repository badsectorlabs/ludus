package cmd

import (
	"strings"

	"github.com/spf13/pflag"
)

// installMode is how a `ludus source add` / `ludus source catalog`
// invocation decides whether to open the interactive picker, apply a
// scripted selection from flags, or fall back to installing everything.
type installMode int

const (
	modeInteractive installMode = iota
	modeScripted
	modeInstallAll
)

// sourceFlags collects everything the source add/catalog commands read off
// the command line. Kept as a struct so selectInstallMode is pure and
// testable without poking package-global flag vars.
type sourceFlags struct {
	ID  string
	Ref string
	URL string

	// Directory is the value of -d. When set, runSourceAdd treats arg as a
	// directory regardless of positional auto-detection.
	Directory string
	// Catalog is true when --catalog is set. runSourceAdd dumps the catalog
	// JSON and exits without committing an install.
	Catalog bool

	All      bool
	NoPrompt bool

	Blueprints       []string
	Templates        []string
	LocalRoles       []string
	LocalCollections []string

	Global bool
	Force  bool
	NoDeps bool
}

// selectInstallMode classifies the invocation. --all and --no-prompt force
// modeInstallAll for backward compatibility with CI scripts. Any selection
// flag (--blueprints/--templates/--source-roles) selects modeScripted.
// Otherwise: TTY → interactive, non-TTY → install-all.
func selectInstallMode(f sourceFlags, isTTY bool) installMode {
	if f.All || f.NoPrompt {
		return modeInstallAll
	}
	if len(f.Blueprints)+len(f.Templates)+len(f.LocalRoles)+len(f.LocalCollections) > 0 {
		return modeScripted
	}
	if !isTTY {
		return modeInstallAll
	}
	return modeInteractive
}

// stringSliceCSV is a pflag.Value that accepts both repeated flags
// (--x a --x b) and CSVs (--x a,b). Items are trimmed; empty items dropped.
type stringSliceCSV []string

func (s *stringSliceCSV) String() string { return strings.Join(*s, ",") }

func (s *stringSliceCSV) Set(v string) error {
	for _, item := range strings.Split(v, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			*s = append(*s, item)
		}
	}
	return nil
}

func (s *stringSliceCSV) Type() string { return "stringSliceCSV" }

// Static guard: stringSliceCSV must satisfy pflag.Value.
var _ pflag.Value = (*stringSliceCSV)(nil)
