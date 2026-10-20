package ludusapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"ludusapi/models"
	"net"
	"net/http/httptest"
	"net/rpc"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

// hostFixture is a PocketBase test app with three ranges:
//   - OWNER has range 10 directly
//   - MEMBER reaches range 11 through a group
//   - OTHER has no range; ADMIN is an admin
//
// Range 12 belongs to nobody.
type hostFixture struct {
	pb                         *pocketbase.PocketBase
	server                     *Server
	owner, member, other, root *core.Record
	admin, superuser           *core.Record
	ranges                     map[int]*core.Record
	vms                        map[int]*core.Record // by range number
}

func newHostFixture(t *testing.T) *hostFixture {
	t.Helper()
	t.Setenv("LUDUS_INSTALL_PATH", t.TempDir())
	previousLogger, previousConfig, previousApp := logger, ServerConfiguration, app
	logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	t.Cleanup(func() { logger, ServerConfiguration, app = previousLogger, previousConfig, previousApp })
	ServerConfiguration.MaxLogHistory = 10
	ServerConfiguration.DataDirectory = t.TempDir()

	pb := pocketbase.NewWithConfig(pocketbase.Config{DefaultDataDir: t.TempDir(), HideStartBanner: true})
	if err := pb.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pb.ResetBootstrapState() })
	if err := pb.RunAllMigrations(); err != nil {
		t.Fatal(err)
	}
	app = pb

	f := &hostFixture{
		pb:     pb,
		server: &Server{Logger: logger, LudusInstallPath: t.TempDir()},
		ranges: map[int]*core.Record{},
		vms:    map[int]*core.Record{},
	}
	for i, number := range []int{10, 11, 12} {
		rangeID := []string{"OWNER", "MEMBER-RANGE", "SPARE"}[i]
		f.ranges[number] = f.save(t, "ranges", map[string]any{"rangeID": rangeID, "rangeNumber": number, "name": rangeID, "rangeState": "SUCCESS"})
		f.vms[number] = f.save(t, "vms", map[string]any{"proxmoxID": 100 + number, "range": f.ranges[number].Id, "name": rangeID + "-vm", "cpu": 2, "ram": 4, "poweredOn": true})
	}
	newUser := func(userID string, number int, admin bool, ranges ...string) *core.Record {
		return f.save(t, "users", map[string]any{
			"email": userID + "@example.com", "password": "host-service-password", "userID": userID,
			"userNumber": number, "isAdmin": admin, "name": userID, "ranges": ranges,
		})
	}
	f.owner = newUser("OWNER", 1, false, f.ranges[10].Id)
	f.member = newUser("MEMBER", 2, false)
	f.other = newUser("OTHER", 3, false)
	f.admin = newUser("ADMIN", 4, true)
	f.root = newUser("ROOT", 5, true)
	f.save(t, "groups", map[string]any{"name": "members", "members": []string{f.member.Id}, "ranges": []string{f.ranges[11].Id}})
	f.superuser = f.save(t, core.CollectionNameSuperusers, map[string]any{"email": "superuser@example.com", "password": "host-service-password"})
	return f
}

func (f *hostFixture) save(t *testing.T, collection string, fields map[string]any) *core.Record {
	t.Helper()
	c, err := f.pb.FindCollectionByNameOrId(collection)
	if err != nil {
		t.Fatal(err)
	}
	record := core.NewRecord(c)
	for key, value := range fields {
		record.Set(key, value)
	}
	if err := f.pb.Save(record); err != nil {
		t.Fatalf("save %s: %v", collection, err)
	}
	return record
}

// event mirrors userAndRangesLookupMiddleware: auth is the authenticated
// record, effective the (possibly impersonated) user, rangeNumber the range.
func (f *hostFixture) event(t *testing.T, auth, effective *core.Record, rangeNumber int) *core.RequestEvent {
	t.Helper()
	e := &core.RequestEvent{App: f.pb}
	e.Request = httptest.NewRequest("GET", "/api/v2/plugins/test/rpc/route", nil)
	e.Response = httptest.NewRecorder()
	e.Auth = auth
	if effective != nil {
		record, err := f.pb.FindRecordById("users", effective.Id)
		if err != nil {
			t.Fatal(err)
		}
		f.pb.ExpandRecord(record, []string{"ranges", "groups"}, nil)
		user := &models.User{}
		user.SetProxyRecord(record)
		e.Set("user", user)
	}
	if rangeNumber != 0 {
		record, err := f.pb.FindRecordById("ranges", f.ranges[rangeNumber].Id)
		if err != nil {
			t.Fatal(err)
		}
		usersRange := &models.Range{}
		usersRange.SetProxyRecord(record)
		e.Set("range", usersRange)
	}
	return e
}

func testPlugin(id string, level protocol.Level, source protocol.Source) *managedPlugin {
	return &managedPlugin{
		manifest: protocol.Manifest{ID: id, Name: id, Level: level, RunAs: protocol.RunAsLudus},
		source:   source,
	}
}

func hostCode(err *protocol.HostError) protocol.ErrorCode {
	if err == nil {
		return ""
	}
	return err.Code
}

func TestHostServicePrincipals(t *testing.T) {
	f := newHostFixture(t)
	standard := newHostService(f.server, testPlugin("standard-plugin", protocol.LevelStandard, protocol.SourceUploaded))
	system := newHostService(f.server, testPlugin("system-plugin", protocol.LevelSystem, protocol.SourceUploaded))

	type principal struct {
		name  string
		event func() *core.RequestEvent // nil: plugin principal
	}
	owner := principal{"owner", func() *core.RequestEvent { return f.event(t, f.owner, f.owner, 10) }}
	member := principal{"member", func() *core.RequestEvent { return f.event(t, f.member, f.member, 11) }}
	other := principal{"other", func() *core.RequestEvent { return f.event(t, f.other, f.other, 0) }}
	admin := principal{"admin", func() *core.RequestEvent { return f.event(t, f.admin, f.admin, 0) }}
	superuser := principal{"superuser", func() *core.RequestEvent { return f.event(t, f.superuser, nil, 0) }}
	// An admin impersonating OTHER with ?userID= is evaluated as OTHER.
	impersonated := principal{"admin as other", func() *core.RequestEvent { return f.event(t, f.admin, f.other, 0) }}
	pluginCall := principal{"plugin", nil}

	begin := func(h *HostService, p principal) (protocol.CallID, func()) {
		if p.event == nil {
			return h.beginPluginCall(context.Background())
		}
		return h.beginRequest(p.event())
	}

	canAccess := func(number int) func(*HostService, protocol.CallID) (*protocol.HostError, any) {
		return func(h *HostService, call protocol.CallID) (*protocol.HostError, any) {
			var reply protocol.BoolReply
			h.CanAccessRange(protocol.RangeArgs{Call: call, Number: number}, &reply)
			return reply.Err, reply.Value
		}
	}
	rangeVMs := func(h *HostService, call protocol.CallID) (*protocol.HostError, any) {
		var reply protocol.VMsReply
		h.RangeVMs(protocol.RangeArgs{Call: call, Number: 10}, &reply)
		return reply.Err, len(reply.VMs)
	}
	accessible := func(h *HostService, call protocol.CallID) (*protocol.HostError, any) {
		var reply protocol.RangesReply
		h.AccessibleRanges(protocol.CallArgs{Call: call}, &reply)
		ids := []string{}
		for _, r := range reply.Ranges {
			ids = append(ids, r.RangeID)
		}
		return reply.Err, ids
	}
	findVM := func(h *HostService, call protocol.CallID) (*protocol.HostError, any) {
		var reply protocol.FindVMReply
		h.FindVM(protocol.FindVMArgs{Call: call, ProxmoxID: 110}, &reply)
		return reply.Err, reply.Range.RangeID
	}
	listRanges := func(h *HostService, call protocol.CallID) (*protocol.HostError, any) {
		var reply protocol.RangesReply
		h.ListRanges(protocol.CallArgs{Call: call}, &reply)
		return reply.Err, len(reply.Ranges)
	}
	rootCredentials := func(h *HostService, call protocol.CallID) (*protocol.HostError, any) {
		var reply protocol.ProxmoxCredentialsReply
		h.RootProxmoxCredentials(protocol.CallArgs{Call: call}, &reply)
		return reply.Err, nil
	}
	quotaOf := func(userID string) func(*HostService, protocol.CallID) (*protocol.HostError, any) {
		return func(h *HostService, call protocol.CallID) (*protocol.HostError, any) {
			var reply protocol.QuotaStatusReply
			h.QuotaStatus(protocol.QuotaStatusArgs{Call: call, UserID: userID}, &reply)
			return reply.Err, reply.Status.QuotaStatus
		}
	}
	listQuotas := func(h *HostService, call protocol.CallID) (*protocol.HostError, any) {
		var reply protocol.UserQuotaStatusesReply
		h.ListQuotaStatus(protocol.CallArgs{Call: call}, &reply)
		ids := []string{}
		for _, status := range reply.Statuses {
			ids = append(ids, status.UserID)
		}
		return reply.Err, ids
	}

	ownerUsage := protocol.QuotaStatus{UsedRAM: 4, UsedCPU: 2, UsedVMs: 1, UsedRanges: 1}
	for _, test := range []struct {
		method string
		host   *HostService
		who    principal
		call   func(*HostService, protocol.CallID) (*protocol.HostError, any)
		code   protocol.ErrorCode
		value  any // nil: not checked
	}{
		{"CanAccessRange direct", standard, owner, canAccess(10), "", true},
		{"CanAccessRange group", standard, member, canAccess(11), "", true},
		{"CanAccessRange other range", standard, member, canAccess(10), "", false},
		{"CanAccessRange no access", standard, other, canAccess(10), "", false},
		{"CanAccessRange admin", standard, admin, canAccess(12), "", true},
		{"CanAccessRange superuser", standard, superuser, canAccess(12), "", true},
		{"CanAccessRange impersonated", standard, impersonated, canAccess(10), "", false},
		{"CanAccessRange plugin principal", system, pluginCall, canAccess(10), protocol.CodeForbidden, nil},

		{"RangeVMs accessible", standard, owner, rangeVMs, "", 1},
		{"RangeVMs inaccessible", standard, other, rangeVMs, protocol.CodeForbidden, nil},
		{"RangeVMs standard plugin principal", standard, pluginCall, rangeVMs, protocol.CodeForbidden, nil},
		{"RangeVMs system plugin principal", system, pluginCall, rangeVMs, "", 1},
		{"RangeVMs system plugin, caller without access", system, other, rangeVMs, protocol.CodeForbidden, nil},

		{"AccessibleRanges direct", standard, owner, accessible, "", []string{"OWNER"}},
		{"AccessibleRanges group", standard, member, accessible, "", []string{"MEMBER-RANGE"}},
		{"AccessibleRanges none", standard, other, accessible, "", []string{}},
		{"AccessibleRanges admin", standard, admin, accessible, "", []string{"OWNER", "MEMBER-RANGE", "SPARE"}},
		{"AccessibleRanges superuser", standard, superuser, accessible, "", []string{"OWNER", "MEMBER-RANGE", "SPARE"}},
		{"AccessibleRanges impersonated", standard, impersonated, accessible, "", []string{}},
		{"AccessibleRanges plugin principal", system, pluginCall, accessible, protocol.CodeForbidden, nil},

		{"FindVM accessible", standard, owner, findVM, "", "OWNER"},
		{"FindVM inaccessible", standard, member, findVM, protocol.CodeForbidden, nil},

		{"ListRanges standard admin", standard, admin, listRanges, protocol.CodeForbidden, nil},
		{"ListRanges standard plugin principal", standard, pluginCall, listRanges, protocol.CodeForbidden, nil},
		{"ListRanges system admin", system, admin, listRanges, "", 3},
		{"ListRanges system superuser", system, superuser, listRanges, "", 3},
		{"ListRanges system non-admin", system, owner, listRanges, protocol.CodeForbidden, nil},
		{"ListRanges system impersonated", system, impersonated, listRanges, protocol.CodeForbidden, nil},
		{"ListRanges system plugin principal", system, pluginCall, listRanges, "", 3},
		{"RootProxmoxCredentials system non-admin", system, owner, rootCredentials, protocol.CodeForbidden, nil},
		{"RootProxmoxCredentials system admin", system, admin, rootCredentials, "", nil},
		{"RootProxmoxCredentials standard plugin principal", standard, pluginCall, rootCredentials, protocol.CodeForbidden, nil},

		{"QuotaStatus own", system, owner, quotaOf(""), "", ownerUsage},
		{"QuotaStatus standard plugin", standard, owner, quotaOf(""), protocol.CodeForbidden, nil},
		{"QuotaStatus plugin principal", system, pluginCall, quotaOf("OWNER"), protocol.CodeForbidden, nil},
		{"QuotaStatus another user", system, other, quotaOf("OWNER"), protocol.CodeForbidden, nil},
		{"QuotaStatus another user as admin", system, admin, quotaOf("OWNER"), "", ownerUsage},
		{"QuotaStatus unknown user", system, admin, quotaOf("NOBODY"), protocol.CodeNotFound, nil},
		{"ListQuotaStatus admin excludes ROOT", system, admin, listQuotas, "", []string{"OWNER", "MEMBER", "OTHER", "ADMIN"}},
		{"ListQuotaStatus non-admin", system, owner, listQuotas, protocol.CodeForbidden, nil},
		{"ListQuotaStatus impersonated", system, impersonated, listQuotas, protocol.CodeForbidden, nil},
		{"ListQuotaStatus standard admin", standard, admin, listQuotas, protocol.CodeForbidden, nil},
	} {
		t.Run(test.method+"/"+test.who.name, func(t *testing.T) {
			call, end := begin(test.host, test.who)
			defer end()
			err, value := test.call(test.host, call)
			if hostCode(err) != test.code {
				t.Fatalf("error %v, want code %q", err, test.code)
			}
			if test.value != nil && !equalValue(value, test.value) {
				t.Fatalf("value %#v, want %#v", value, test.value)
			}
		})
	}
}

func equalValue(a, b any) bool {
	as, aok := a.([]string)
	bs, bok := b.([]string)
	if aok && bok {
		if len(as) != len(bs) {
			return false
		}
		for i := range as {
			if as[i] != bs[i] {
				return false
			}
		}
		return true
	}
	return a == b
}

func TestHostServiceCallExpiry(t *testing.T) {
	f := newHostFixture(t)
	h := newHostService(f.server, testPlugin("standard-plugin", protocol.LevelStandard, protocol.SourceUploaded))
	other := newHostService(f.server, testPlugin("other-plugin", protocol.LevelStandard, protocol.SourceUploaded))

	call, end := h.beginRequest(f.event(t, f.owner, f.owner, 10))
	if len(call) != 32 {
		t.Fatalf("call ID %q is not 128-bit hex", call)
	}
	var reply protocol.BoolReply
	other.CanAccessRange(protocol.RangeArgs{Call: call, Number: 10}, &reply)
	if hostCode(reply.Err) != protocol.CodeExpired {
		t.Fatalf("another plugin used the call ID: %v", reply.Err)
	}
	end()
	h.CanAccessRange(protocol.RangeArgs{Call: call, Number: 10}, &reply)
	if hostCode(reply.Err) != protocol.CodeExpired {
		t.Fatalf("expired call: %v", reply.Err)
	}
	var license protocol.LicenseReply
	h.License(protocol.CallArgs{Call: "unknown"}, &license)
	if hostCode(license.Err) != protocol.CodeExpired || !errors.Is(license.Err, &protocol.HostError{Code: protocol.CodeExpired}) {
		t.Fatalf("unknown call: %v", license.Err)
	}
}

func TestHostServiceLicensingIdentity(t *testing.T) {
	f := newHostFixture(t)
	for _, test := range []struct {
		name   string
		plugin *managedPlugin
		want   bool
	}{
		{"licensed enterprise", testPlugin(enterprisePluginID, protocol.LevelSystem, protocol.SourceLicensed), true},
		{"root-placed enterprise", testPlugin(enterprisePluginID, protocol.LevelSystem, protocol.SourceServer), true},
		{"uploaded enterprise ID", testPlugin(enterprisePluginID, protocol.LevelSystem, protocol.SourceUploaded), false},
		{"licensed other ID", testPlugin(antisandboxPluginID, protocol.LevelSystem, protocol.SourceLicensed), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.plugin.manifest.Name = "Ludus Enterprise"
			if got := isEnterprisePlugin(test.plugin); got != test.want {
				t.Fatalf("isEnterprisePlugin = %t, want %t", got, test.want)
			}
			if test.want {
				return
			}
			h := newHostService(f.server, test.plugin)
			call, end := h.beginPluginCall(context.Background())
			defer end()
			var reply protocol.EmptyReply
			h.SetLicenseState(protocol.SetLicenseStateArgs{Call: call, License: protocol.License{Valid: true}}, &reply)
			if hostCode(reply.Err) != protocol.CodeForbidden {
				t.Fatalf("SetLicenseState: %v", reply.Err)
			}
			h.RequestAddOn(protocol.AddOnArgs{Call: call, Name: antisandboxPluginID}, &reply)
			if hostCode(reply.Err) != protocol.CodeForbidden {
				t.Fatalf("RequestAddOn: %v", reply.Err)
			}
		})
	}
}

func TestHostServiceReadRangeFile(t *testing.T) {
	f := newHostFixture(t)
	h := newHostService(f.server, testPlugin("standard-plugin", protocol.LevelStandard, protocol.SourceUploaded))
	rangesDir := filepath.Join(f.server.LudusInstallPath, "ranges")
	for _, dir := range []string{"OWNER", "MEMBER-RANGE"} {
		if err := os.MkdirAll(filepath.Join(rangesDir, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(rangesDir, "OWNER", "range-config.yml"), []byte("ludus: []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rangesDir, "OWNER", "ansible.log"), []byte("0123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(t.TempDir(), "secret.yml")
	if err := os.WriteFile(secret, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	// MEMBER's range config escapes <install>/ranges through a symlink.
	if err := os.Symlink(secret, filepath.Join(rangesDir, "MEMBER-RANGE", "range-config.yml")); err != nil {
		t.Fatal(err)
	}

	read := func(e *core.RequestEvent, number int, file protocol.RangeFile, max int64) protocol.BytesReply {
		call, end := h.beginRequest(e)
		defer end()
		var reply protocol.BytesReply
		h.ReadRangeFile(protocol.ReadRangeFileArgs{Call: call, Number: number, File: file, MaxBytes: max}, &reply)
		return reply
	}
	ownerEvent := f.event(t, f.owner, f.owner, 10)
	if reply := read(ownerEvent, 10, protocol.RangeFileConfig, 0); reply.Err != nil || string(reply.Value) != "ludus: []\n" {
		t.Fatalf("config: %q %v", reply.Value, reply.Err)
	}
	if reply := read(ownerEvent, 10, protocol.RangeFileAnsibleLog, 4); reply.Err != nil || string(reply.Value) != "6789" {
		t.Fatalf("log tail: %q %v", reply.Value, reply.Err)
	}
	if reply := read(ownerEvent, 10, protocol.RangeFileConfig, 4); hostCode(reply.Err) != protocol.CodeInvalid {
		t.Fatalf("oversized config: %v", reply.Err)
	}
	if reply := read(f.event(t, f.other, f.other, 0), 10, protocol.RangeFileConfig, 0); hostCode(reply.Err) != protocol.CodeForbidden {
		t.Fatalf("inaccessible range: %v", reply.Err)
	}
	if reply := read(f.event(t, f.member, f.member, 11), 11, protocol.RangeFileConfig, 0); hostCode(reply.Err) != protocol.CodeInvalid {
		t.Fatalf("symlink escape: %q %v", reply.Value, reply.Err)
	}
	// Range 12 has no range-config.yml yet.
	if reply := read(f.event(t, f.admin, f.admin, 0), 12, protocol.RangeFileConfig, 0); hostCode(reply.Err) != protocol.CodeNotFound {
		t.Fatalf("missing config: %v", reply.Err)
	}
	// MaxBytes 0 reads the whole file up to the 1 MiB default.
	large := strings.Repeat("a", defaultHostReadBytes)
	if err := os.WriteFile(filepath.Join(rangesDir, "OWNER", "range-config.yml"), []byte(large), 0600); err != nil {
		t.Fatal(err)
	}
	if reply := read(ownerEvent, 10, protocol.RangeFileConfig, 0); reply.Err != nil || string(reply.Value) != large {
		t.Fatalf("default-size config: %d bytes, %v", len(reply.Value), reply.Err)
	}
	if err := os.WriteFile(filepath.Join(rangesDir, "OWNER", "range-config.yml"), []byte(large+"a"), 0600); err != nil {
		t.Fatal(err)
	}
	if reply := read(ownerEvent, 10, protocol.RangeFileConfig, 0); hostCode(reply.Err) != protocol.CodeInvalid {
		t.Fatalf("config over the default: %v", reply.Err)
	}
}

func TestHostServiceProxmoxCredentials(t *testing.T) {
	f := newHostFixture(t)
	ServerConfiguration.DatabaseEncryptionKey = "0123456789abcdef0123456789abcdef"
	ServerConfiguration.ProxmoxEndpoints = []string{"https://pve1.example:8006", "https://pve2.example:8006"}
	ServerConfiguration.ProxmoxTokenID = "root@pam!ludus"
	ServerConfiguration.ProxmoxTokenSecret = "root-secret"
	ServerConfiguration.ProxmoxInvalidCert = true
	encrypted, err := EncryptStringForDatabase("owner-secret")
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range []*core.Record{f.owner, f.root} {
		record.Set("proxmoxTokenID", record.GetString("userID")+"@pve!ludus-token")
		record.Set("proxmoxTokenSecret", encrypted)
		if err := f.pb.Save(record); err != nil {
			t.Fatal(err)
		}
	}
	h := newHostService(f.server, testPlugin("system-plugin", protocol.LevelSystem, protocol.SourceUploaded))
	credentials := func(e *core.RequestEvent, root bool) protocol.ProxmoxCredentialsReply {
		call, end := h.beginRequest(e)
		defer end()
		var reply protocol.ProxmoxCredentialsReply
		if root {
			h.RootProxmoxCredentials(protocol.CallArgs{Call: call}, &reply)
		} else {
			h.ProxmoxCredentials(protocol.CallArgs{Call: call}, &reply)
		}
		return reply
	}

	user := credentials(f.event(t, f.owner, f.owner, 10), false)
	want := protocol.ProxmoxCredentials{Endpoints: ServerConfiguration.ProxmoxEndpoints, TokenID: "OWNER@pve!ludus-token", TokenSecret: "owner-secret", InsecureTLS: true}
	if user.Err != nil || !slices.Equal(user.Credentials.Endpoints, want.Endpoints) || user.Credentials.TokenID != want.TokenID ||
		user.Credentials.TokenSecret != want.TokenSecret || !user.Credentials.InsecureTLS {
		t.Fatalf("user credentials: %+v %v", user.Credentials, user.Err)
	}
	if reply := credentials(f.event(t, f.root, f.root, 0), false); hostCode(reply.Err) != protocol.CodeForbidden {
		t.Fatalf("ROOT credentials: %v", reply.Err)
	}
	root := credentials(f.event(t, f.admin, f.admin, 0), true)
	if root.Err != nil || !slices.Equal(root.Credentials.Endpoints, want.Endpoints) ||
		root.Credentials.TokenID != "root@pam!ludus" || root.Credentials.TokenSecret != "root-secret" {
		t.Fatalf("root credentials for an admin caller: %+v %v", root.Credentials, root.Err)
	}
}

// Quota patches travel over gob, which drops zero values; setting a quota to
// 0 (unlimited) must still reach the database through the real RPC path.
func TestHostServiceQuotaPatchOverRPC(t *testing.T) {
	f := newHostFixture(t)
	f.owner.Set("quotaRAM", 16)
	f.owner.Set("quotaCPU", 8)
	if err := f.pb.Save(f.owner); err != nil {
		t.Fatal(err)
	}
	group, err := f.pb.FindFirstRecordByData("groups", "name", "members")
	if err != nil {
		t.Fatal(err)
	}
	group.Set("defaultQuotaVMs", 5)
	if err := f.pb.Save(group); err != nil {
		t.Fatal(err)
	}

	h := newHostService(f.server, testPlugin("system-plugin", protocol.LevelSystem, protocol.SourceUploaded))
	server := rpc.NewServer()
	if err := server.RegisterName(protocol.HostServiceName, h); err != nil {
		t.Fatal(err)
	}
	serverConn, clientConn := net.Pipe()
	go server.ServeConn(serverConn)
	client := protocol.NewHostClient(rpc.NewClient(clientConn))
	t.Cleanup(func() { client.Close() })

	call, end := h.beginRequest(f.event(t, f.admin, f.admin, 0))
	defer end()
	zero, negative := 0, -1
	if errs, err := client.SetUserQuota(call, []string{"OWNER", "NOBODY"}, protocol.NewQuotaPatch(&zero, nil, nil, nil)); err != nil ||
		!slices.Equal(errs, []string{"User NOBODY not found"}) {
		t.Fatalf("SetUserQuota: %v %v", errs, err)
	}
	owner, err := f.pb.FindRecordById("users", f.owner.Id)
	if err != nil || owner.GetInt("quotaRAM") != 0 || owner.GetInt("quotaCPU") != 8 {
		t.Fatalf("user quotas: ram=%d cpu=%d %v", owner.GetInt("quotaRAM"), owner.GetInt("quotaCPU"), err)
	}
	if _, err := client.SetGroupDefaultQuota(call, []string{"members"}, protocol.NewQuotaPatch(nil, nil, &zero, nil)); err != nil {
		t.Fatal(err)
	}
	group, err = f.pb.FindRecordById("groups", group.Id)
	if err != nil || group.GetInt("defaultQuotaVMs") != 0 {
		t.Fatalf("group quota: %d %v", group.GetInt("defaultQuotaVMs"), err)
	}
	if _, err := client.SetUserQuota(call, []string{"OWNER"}, protocol.NewQuotaPatch(nil, &negative, nil, nil)); !errors.Is(err, &protocol.HostError{Code: protocol.CodeInvalid}) {
		t.Fatalf("negative quota: %v", err)
	}
}

func TestHostServiceSetRangeInactivityTimeout(t *testing.T) {
	f := newHostFixture(t)
	h := newHostService(f.server, testPlugin("standard-plugin", protocol.LevelStandard, protocol.SourceUploaded))
	set := func(e *core.RequestEvent, number int, timeout string) *protocol.HostError {
		call, end := h.beginRequest(e)
		defer end()
		var reply protocol.EmptyReply
		h.SetRangeInactivityTimeout(protocol.SetInactivityTimeoutArgs{Call: call, Number: number, Timeout: timeout}, &reply)
		return reply.Err
	}
	stored := func() string {
		record, err := f.pb.FindRecordById("ranges", f.ranges[10].Id)
		if err != nil {
			t.Fatal(err)
		}
		return record.GetString("inactivityShutdownTimeout")
	}
	for _, timeout := range []string{"30m", "0", "0s", ""} {
		if err := set(f.event(t, f.owner, f.owner, 10), 10, timeout); err != nil {
			t.Fatalf("timeout %q: %v", timeout, err)
		}
		if got := stored(); got != timeout {
			t.Fatalf("stored %q, want %q", got, timeout)
		}
	}
	for _, timeout := range []string{"-5m", "soon"} {
		if err := set(f.event(t, f.owner, f.owner, 10), 10, timeout); hostCode(err) != protocol.CodeInvalid {
			t.Fatalf("timeout %q: %v", timeout, err)
		}
	}
	if err := set(f.event(t, f.other, f.other, 0), 10, "1h"); hostCode(err) != protocol.CodeForbidden {
		t.Fatalf("inaccessible range: %v", err)
	}
	if got := stored(); got != "" {
		t.Fatalf("rejected calls changed the timeout to %q", got)
	}
}
