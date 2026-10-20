package ludusapi

import (
	"errors"
	"ludusapi/models"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

func TestPluginRequestCredentialsAndRecords(t *testing.T) {
	f := newHostFixture(t)
	route := protocol.Route{Name: "status", Method: http.MethodPost, Pattern: "/status"}
	newEvent := func() *core.RequestEvent {
		e := f.event(t, f.admin, f.other, 10)
		e.Request = httptest.NewRequest(http.MethodPost, "/api/v2/plugins/x/rpc/status?userID=OTHER", strings.NewReader("payload"))
		for _, name := range []string{"Authorization", "X-API-KEY", "Cookie", "X-Trace"} {
			e.Request.Header.Set(name, "value")
		}
		return e
	}

	request, err := newPluginRequest(newEvent(), "call", route, false)
	if err != nil {
		t.Fatal(err)
	}
	header := http.Header(request.Header)
	for _, name := range pluginCredentialHeaders {
		if header.Get(name) != "" {
			t.Fatalf("credential header %s forwarded without forwardCallerCredentials", name)
		}
	}
	if header.Get("X-Trace") != "value" || string(request.Body) != "payload" || request.Route != "status" ||
		request.Path != "/api/v2/plugins/x/rpc/status" || request.RawQuery != "userID=OTHER" || request.Call != "call" {
		t.Fatalf("request: %+v", request)
	}
	if request.Auth.UserID != "ADMIN" || !request.Auth.IsAdmin || request.User == nil || request.User.UserID != "OTHER" ||
		request.User.IsAdmin || request.Range == nil || request.Range.RangeID != "OWNER" || request.Range.RecordID != f.ranges[10].Id {
		t.Fatalf("impersonated identity: auth=%+v user=%+v range=%+v", request.Auth, request.User, request.Range)
	}

	forwarded, err := newPluginRequest(newEvent(), "call", route, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range pluginCredentialHeaders {
		if http.Header(forwarded.Header).Get(name) != "value" {
			t.Fatalf("credential header %s dropped despite forwardCallerCredentials", name)
		}
	}

	// The middleware's ROOT pseudo-range is unsaved.
	e := f.event(t, f.root, f.root, 0)
	rangesCollection, err := f.pb.FindCollectionByNameOrId("ranges")
	if err != nil {
		t.Fatal(err)
	}
	pseudo := &models.Range{}
	pseudo.SetProxyRecord(core.NewRecord(rangesCollection))
	pseudo.SetRangeId("ROOT")
	pseudo.SetRangeNumber(1)
	e.Set("range", pseudo)
	rootRequest, err := newPluginRequest(e, "call", route, false)
	if err != nil {
		t.Fatal(err)
	}
	if rootRequest.Range == nil || rootRequest.Range.RecordID != "" || rootRequest.Range.RangeID != "ROOT" {
		t.Fatalf("ROOT pseudo-range: %+v", rootRequest.Range)
	}

	super, err := newPluginRequest(f.event(t, f.superuser, nil, 0), "call", route, false)
	if err != nil {
		t.Fatal(err)
	}
	if !super.Auth.IsSuperuser || super.User != nil || super.Range != nil {
		t.Fatalf("superuser request: %+v", super)
	}
}

func TestPluginRequestBodyLimits(t *testing.T) {
	previous := pluginMaxBodyBytes
	pluginMaxBodyBytes = 8
	t.Cleanup(func() { pluginMaxBodyBytes = previous })
	route := protocol.Route{Name: "upload"}
	event := func(body string) (*core.RequestEvent, *httptest.ResponseRecorder) {
		recorder := httptest.NewRecorder()
		e := &core.RequestEvent{}
		e.Request = httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader(body))
		e.Response = recorder
		return e, recorder
	}

	e, _ := event("12345678")
	if request, err := newPluginRequest(e, "call", route, false); err != nil || string(request.Body) != "12345678" {
		t.Fatalf("body at the limit: %q %v", request.Body, err)
	}
	e, _ = event("123456789")
	if _, err := newPluginRequest(e, "call", route, false); !errors.Is(err, errPluginBodyTooLarge) {
		t.Fatalf("oversized body: %v", err)
	}

	e, recorder := event("")
	if err := writePluginResponse(e, protocol.Response{Status: http.StatusOK, Body: []byte("123456789")}); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusBadGateway || strings.Contains(recorder.Body.String(), "12345678") {
		t.Fatalf("oversized response: %d %q", recorder.Code, recorder.Body.String())
	}

	e, recorder = event("")
	if err := writePluginResponse(e, protocol.Response{Status: http.StatusCreated, Header: map[string][]string{"X-Plugin": {"yes"}}, Body: []byte("12345678")}); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusCreated || recorder.Header().Get("X-Plugin") != "yes" || recorder.Body.String() != "12345678" {
		t.Fatalf("response: %d %v %q", recorder.Code, recorder.Header(), recorder.Body.String())
	}
}
