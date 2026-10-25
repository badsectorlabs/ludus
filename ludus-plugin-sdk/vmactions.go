package ludus

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

// VMAction is one action the Ludus GUI offers on a VM of the selected range.
// Selecting it opens the plugin's App in a new browser tab. The plugin owns
// the icon: a standard IBM Carbon glyph by name, or its own SVG.
type VMAction struct {
	ID        string `json:"id"` // passed back to VMActions.Launch
	ProxmoxID int    `json:"proxmoxID"`
	Label     string `json:"label"`
	// Icon names an IBM Carbon icon, such as "Terminal" or "Screen"
	// (https://carbondesignsystem.com/elements/icons/library/). Empty, or a
	// name Carbon does not have, draws a generic launch icon.
	Icon string `json:"icon,omitempty"`
	// IconSVG is the plugin's own monochrome icon, used instead of Icon: an
	// SVG document (see protocol.ValidVMActionIconSVG). The GUI draws it as a
	// CSS mask, so only its shapes' alpha matters; their colors are ignored
	// and nothing in it runs.
	IconSVG string `json:"iconSVG,omitempty"`
	// Color tints either icon: #rgb or #rrggbb. Empty uses the GUI's action
	// color.
	Color string `json:"color,omitempty"`
}

// validate reports a declaration error the plugin author must fix.
func (a VMAction) validate() error {
	if a.Icon != "" && !protocol.ValidVMActionIconName(a.Icon) {
		return fmt.Errorf("VM action %q: Icon must be a Carbon icon name such as \"Terminal\" (letters, digits, underscores)", a.ID)
	}
	if a.IconSVG != "" && !protocol.ValidVMActionIconSVG(a.IconSVG) {
		return fmt.Errorf("VM action %q: IconSVG must be an <svg> document of at most %d bytes", a.ID, protocol.MaxVMActionIconSVG)
	}
	if a.Color != "" && !protocol.ValidVMActionColor(a.Color) {
		return fmt.Errorf("VM action %q: Color must be #rgb or #rrggbb", a.ID)
	}
	return nil
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
	for _, action := range actions {
		if err := action.validate(); err != nil {
			Logger().Error("invalid VM action", "error", err)
			Error(w, http.StatusInternalServerError, err.Error())
			return
		}
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
