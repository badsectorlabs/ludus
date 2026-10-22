package ludus

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

// VMAction is one action the Ludus GUI offers on a VM of the selected range.
// Selecting it opens the plugin's App in a new browser tab.
type VMAction struct {
	ID        string `json:"id"` // passed back to VMActions.Launch
	ProxmoxID int    `json:"proxmoxID"`
	Label     string `json:"label"`
	Icon      string `json:"icon"` // protocol.VMActionIcon*
}

// VMActions lets the GUI offer per-VM app launches in the range editor. The
// SDK serves them on the plugin-scoped routes protocol.VMActionsPattern and
// protocol.VMActionLaunchPattern; c.Range is the range the GUI selected,
// already access-checked by the host. VMActions requires App.
type VMActions struct {
	// List returns the actions available on c.Range's VMs.
	List func(c *Call) ([]VMAction, error)
	// Launch opens action id for the caller, normally with c.OpenApp.
	Launch func(c *Call, id string) (*AppLaunch, error)
}

func (a *VMActions) routes() []Route {
	return []Route{
		{Name: "LudusVMActions", Method: http.MethodGet, Pattern: protocol.VMActionsPattern, Handler: a.list},
		{Name: "LudusVMActionLaunch", Method: http.MethodPost, Pattern: protocol.VMActionLaunchPattern, Handler: a.launch},
	}
}

func (a *VMActions) list(w http.ResponseWriter, r *http.Request) {
	c := CallFrom(r)
	if c == nil || c.Range == nil || c.Range.RecordID == "" {
		Error(w, http.StatusBadRequest, "select a range")
		return
	}
	actions, err := a.List(c)
	if err != nil {
		vmActionError(w, err)
		return
	}
	if actions == nil {
		actions = []VMAction{}
	}
	JSON(w, http.StatusOK, map[string]any{"actions": actions})
}

func (a *VMActions) launch(w http.ResponseWriter, r *http.Request) {
	c := CallFrom(r)
	if c == nil || c.Range == nil || c.Range.RecordID == "" {
		Error(w, http.StatusBadRequest, "select a range")
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || body.ID == "" || len(body.ID) > 256 {
		Error(w, http.StatusBadRequest, "invalid VM action")
		return
	}
	launch, err := a.Launch(c, body.ID)
	if err != nil {
		vmActionError(w, err)
		return
	}
	JSON(w, http.StatusOK, map[string]any{"url": launch.URL, "expires": launch.Expires.UnixMilli()})
}

func vmActionError(w http.ResponseWriter, err error) {
	var hostErr *protocol.HostError
	if errors.As(err, &hostErr) {
		HostError(w, err)
		return
	}
	Error(w, http.StatusConflict, err.Error())
}
