package ludusapi

import (
	"errors"
	"fmt"
	"io"
	"ludusapi/models"
	"net/http"

	"github.com/pocketbase/pocketbase/core"
	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

// pluginMaxBodyBytes is protocol.MaxBodyBytes; tests lower it.
var pluginMaxBodyBytes int64 = protocol.MaxBodyBytes

// errPluginBodyTooLarge reports a request body over protocol.MaxBodyBytes.
// Callers answer 413 without reaching the plugin.
var errPluginBodyTooLarge = errors.New("plugin request body exceeds the size limit")

// Caller credential headers reach a plugin only when its approved manifest
// declares forwardCallerCredentials.
var pluginCredentialHeaders = []string{"Authorization", "X-API-KEY", "Cookie"}

// newPluginRequest builds the plugin's view of a request from the event the
// middleware already resolved: the authenticated principal, the effective
// user, and the selected range.
func newPluginRequest(e *core.RequestEvent, call protocol.CallID, route protocol.Route, forwardCredentials bool) (protocol.Request, error) {
	var body []byte
	if e.Request.Body != nil {
		var err error
		body, err = io.ReadAll(http.MaxBytesReader(e.Response, e.Request.Body, pluginMaxBodyBytes))
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				return protocol.Request{}, errPluginBodyTooLarge
			}
			return protocol.Request{}, fmt.Errorf("read plugin request body: %w", err)
		}
	}

	header := e.Request.Header.Clone()
	if header == nil {
		header = http.Header{}
	}
	if !forwardCredentials {
		for _, name := range pluginCredentialHeaders {
			header.Del(name)
		}
	}

	request := protocol.Request{
		Call:       call,
		Route:      route.Name,
		Method:     e.Request.Method,
		Path:       e.Request.URL.Path,
		RawQuery:   e.Request.URL.RawQuery,
		Header:     header,
		Body:       body,
		RemoteAddr: e.Request.RemoteAddr,
	}
	if e.Auth != nil {
		request.Auth = pluginPrincipal(e.Auth)
	}
	if user, ok := e.Get("user").(*models.User); ok && user != nil && user.Record != nil {
		protocolUser := pluginUser(user)
		request.User = &protocolUser
	}
	if usersRange, ok := e.Get("range").(*models.Range); ok && usersRange != nil && usersRange.Record != nil {
		protocolRange := pluginRange(usersRange)
		request.Range = &protocolRange
	}
	return request, nil
}

// writePluginResponse writes a plugin response, or 502 when the plugin
// returned more than protocol.MaxBodyBytes; a truncated body is never sent.
func writePluginResponse(e *core.RequestEvent, response protocol.Response) error {
	if int64(len(response.Body)) > pluginMaxBodyBytes {
		return JSONError(e, http.StatusBadGateway, "plugin response exceeds the size limit")
	}
	for name, values := range response.Header {
		for _, value := range values {
			e.Response.Header().Add(name, value)
		}
	}
	status := response.Status
	if status == 0 {
		status = http.StatusOK
	}
	e.Response.WriteHeader(status)
	_, err := e.Response.Write(response.Body)
	return err
}

func pluginPrincipal(auth *core.Record) protocol.Principal {
	principal := protocol.Principal{
		RecordID:    auth.Id,
		UserID:      auth.GetString("userID"),
		Name:        auth.GetString("name"),
		IsAdmin:     auth.GetBool("isAdmin"),
		IsSuperuser: auth.IsSuperuser(),
	}
	if principal.Name == "" && principal.IsSuperuser {
		principal.Name = auth.Email()
	}
	return principal
}

func pluginUser(user *models.User) protocol.User {
	return protocol.User{
		RecordID:        user.Id,
		UserID:          user.UserId(),
		Name:            user.Name(),
		ProxmoxUsername: user.ProxmoxUsername(),
		DefaultRangeID:  user.DefaultRangeId(),
		IsAdmin:         user.IsAdmin(),
	}
}

// pluginRange converts a range record. The ROOT pseudo-range built by the
// middleware is unsaved, so its RecordID is "".
func pluginRange(usersRange *models.Range) protocol.Range {
	routerVMName, _ := GetRouterVMName(usersRange)
	return protocol.Range{
		RecordID:                  usersRange.Id,
		RangeID:                   usersRange.RangeId(),
		Name:                      usersRange.Name(),
		Number:                    usersRange.RangeNumber(),
		TestingEnabled:            usersRange.TestingEnabled(),
		State:                     usersRange.RangeState(),
		RouterVMName:              routerVMName,
		InactivityShutdownTimeout: usersRange.InactivityShutdownTimeout(),
		LastActive:                usersRange.LastActive().Time(),
	}
}
