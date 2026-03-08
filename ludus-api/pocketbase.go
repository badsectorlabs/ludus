package ludusapi

import (
	"fmt"
	"ludusapi/models"

	"github.com/pocketbase/dbx"
)

func getVMsForRange(rangeID string) ([]*models.VMs, error) {
	rangeRecord, err := app.FindFirstRecordByData("ranges", "rangeID", rangeID)
	if err != nil {
		return nil, fmt.Errorf("error finding range: %w", err)
	}
	rangeObj := &models.Range{}
	rangeObj.SetProxyRecord(rangeRecord)
	rangeVMs, err := app.FindAllRecords("vms", dbx.NewExp("range = {:rangeID}", dbx.Params{"rangeID": rangeObj.Id}))
	if err != nil {
		return nil, fmt.Errorf("error finding VMs for range: %w", err)
	}
	var vms []*models.VMs
	for _, rangeVM := range rangeVMs {
		rangeVMObj := &models.VMs{}
		rangeVMObj.SetProxyRecord(rangeVM)
		vms = append(vms, rangeVMObj)
	}
	return vms, nil
}
