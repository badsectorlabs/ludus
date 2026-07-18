package migrations

import (
	"fmt"
	"sort"

	"github.com/pocketbase/pocketbase/core"

	m "github.com/pocketbase/pocketbase/migrations"
)

// sourceIDRow is the minimal projection planSourceIDRenames needs.
type sourceIDRow struct {
	ID       string
	SourceID string
}

// planSourceIDRenames assigns a globally-unique sourceID to each row. Rows are
// processed in the given (deterministic) order; the first occurrence of a
// sourceID keeps it, and later duplicates get "-2", "-3", ... skipping any id
// that already exists. Distinct ids are never renamed. Returns recordID ->
// newSourceID only for the rows that change.
func planSourceIDRenames(rows []sourceIDRow) map[string]string {
	// Seed with every original id so a generated suffix never collides with an
	// existing distinct id (e.g. a literal "bsl-2").
	used := map[string]bool{}
	for _, r := range rows {
		used[r.SourceID] = true
	}
	seen := map[string]bool{}
	renames := map[string]string{}
	for _, r := range rows {
		if !seen[r.SourceID] {
			seen[r.SourceID] = true // first occurrence keeps its id
			continue
		}
		for n := 2; ; n++ {
			cand := fmt.Sprintf("%s-%d", r.SourceID, n)
			if !used[cand] {
				used[cand] = true
				renames[r.ID] = cand
				break
			}
		}
	}
	return renames
}

func init() {
	m.Register(func(app core.App) error {
		c, err := app.FindCollectionByNameOrId("sources")
		if err != nil {
			return err
		}

		records, err := app.FindAllRecords("sources")
		if err != nil {
			return err
		}
		// Deterministic order: oldest first keeps the original slug; later
		// duplicates get suffixed. Tie-break on record id.
		sort.SliceStable(records, func(i, j int) bool {
			ci := records[i].GetDateTime("created").Time()
			cj := records[j].GetDateTime("created").Time()
			if ci.Equal(cj) {
				return records[i].Id < records[j].Id
			}
			return ci.Before(cj)
		})

		rows := make([]sourceIDRow, len(records))
		for i, r := range records {
			rows[i] = sourceIDRow{ID: r.Id, SourceID: r.GetString("sourceID")}
		}
		renames := planSourceIDRenames(rows)
		for _, r := range records {
			if newID, ok := renames[r.Id]; ok {
				r.Set("sourceID", newID)
				if err := app.Save(r); err != nil {
					return err
				}
			}
		}

		c.RemoveIndex("idx_sources_owner_sourceID_unique")
		c.AddIndex("idx_sources_sourceID_unique", true, "sourceID", "")
		return app.Save(c)
	}, func(app core.App) error {
		// Down: restore the per-owner index. Renamed slugs are not reverted
		// (the rename is lossy); only the uniqueness shape is restored.
		c, err := app.FindCollectionByNameOrId("sources")
		if err != nil {
			return err
		}
		c.RemoveIndex("idx_sources_sourceID_unique")
		c.AddIndex("idx_sources_owner_sourceID_unique", true, "owner, sourceID", "")
		return app.Save(c)
	})
}
