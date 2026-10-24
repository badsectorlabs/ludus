package ludusapi

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// Overlapping inventory refreshes of one range must leave exactly one row per
// VM, and readers must only ever see one complete refresh, never a mix.
func TestReplaceRangeVMsConcurrentRefreshes(t *testing.T) {
	f := newHostFixture(t)
	rangeRecord := f.ranges[10]
	collection, err := f.pb.FindCollectionByNameOrId("vms")
	if err != nil {
		t.Fatal(err)
	}
	// Each refresh names its VMs after its generation.
	snapshot := func(generation int) []*core.Record {
		records := make([]*core.Record, 0, 3)
		for i := range 3 {
			record := core.NewRecord(collection)
			record.Set("proxmoxID", 200+i)
			record.Set("range", rangeRecord.Id)
			record.Set("name", fmt.Sprintf("gen%d-vm%d", generation, i))
			record.Set("cpu", 2)
			record.Set("ram", 4)
			records = append(records, record)
		}
		return records
	}
	type row struct {
		Name      string `db:"name"`
		ProxmoxID int    `db:"proxmoxID"`
	}
	rangeRows := func() []row {
		var rows []row
		if err := f.pb.DB().NewQuery("SELECT name, proxmoxID FROM vms WHERE range = {:range}").
			Bind(dbx.Params{"range": rangeRecord.Id}).All(&rows); err != nil {
			t.Error(err)
		}
		return rows
	}
	// complete reports whether rows are one whole refresh: the fixture's
	// single VM, or three VMs of one generation with distinct Proxmox IDs.
	complete := func(rows []row) bool {
		if len(rows) == 1 && rows[0].Name == "OWNER-vm" {
			return true
		}
		if len(rows) != 3 {
			return false
		}
		generation, _, _ := strings.Cut(rows[0].Name, "-")
		ids := map[int]bool{}
		for _, r := range rows {
			if g, _, _ := strings.Cut(r.Name, "-"); g != generation {
				return false
			}
			ids[r.ProxmoxID] = true
		}
		return len(ids) == 3
	}

	done := make(chan struct{})
	var torn [][]row
	var reader sync.WaitGroup
	reader.Add(1)
	go func() {
		defer reader.Done()
		for {
			select {
			case <-done:
				return
			default:
				if rows := rangeRows(); !complete(rows) && len(torn) < 3 {
					torn = append(torn, rows)
				}
			}
		}
	}()
	var refreshes sync.WaitGroup
	for generation := range 8 {
		refreshes.Add(1)
		go func() {
			defer refreshes.Done()
			if count, err := replaceRangeVMs(f.pb, rangeRecord.Id, snapshot(generation)); err != nil || count != 3 {
				t.Errorf("replaceRangeVMs = %d, %v", count, err)
			}
		}()
	}
	refreshes.Wait()
	close(done)
	reader.Wait()

	for _, rows := range torn {
		t.Errorf("a reader saw a partial or mixed refresh: %+v", rows)
	}
	if rows := rangeRows(); len(rows) != 3 || !complete(rows) {
		t.Fatalf("rows after concurrent refreshes = %+v, want one complete refresh", rows)
	}
	stored, err := f.pb.FindRecordById("ranges", rangeRecord.Id)
	if err != nil || stored.GetInt("numberOfVMs") != 3 {
		t.Fatalf("numberOfVMs = %d, %v", stored.GetInt("numberOfVMs"), err)
	}
	// Other ranges keep their rows.
	if _, err := f.pb.FindRecordById("vms", f.vms[11].Id); err != nil {
		t.Fatalf("refresh of range 10 removed range 11's VM: %v", err)
	}
}

// Duplicate rows left by an earlier refresh are replaced by the next one.
func TestReplaceRangeVMsReplacesLeftoverDuplicates(t *testing.T) {
	f := newHostFixture(t)
	rangeRecord := f.ranges[10]
	for range 2 {
		f.save(t, "vms", map[string]any{"proxmoxID": 110, "range": rangeRecord.Id, "name": "OWNER-vm", "cpu": 2, "ram": 4})
	}
	collection, err := f.pb.FindCollectionByNameOrId("vms")
	if err != nil {
		t.Fatal(err)
	}
	record := core.NewRecord(collection)
	record.Set("proxmoxID", 110)
	record.Set("range", rangeRecord.Id)
	record.Set("name", "OWNER-vm")
	if count, err := replaceRangeVMs(f.pb, rangeRecord.Id, []*core.Record{record}); err != nil || count != 1 {
		t.Fatalf("replaceRangeVMs = %d, %v", count, err)
	}
	rows, err := f.pb.FindAllRecords("vms", dbx.HashExp{"range": rangeRecord.Id})
	if err != nil || len(rows) != 1 || rows[0].Id != record.Id {
		t.Fatalf("rows after refresh = %d, %v; want only the refreshed VM", len(rows), err)
	}
}
