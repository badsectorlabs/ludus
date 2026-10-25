package ludusapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"ludusapi/models"
	"net/rpc"
	"runtime/debug"
	"sync"

	hcplugin "github.com/hashicorp/go-plugin"
	"github.com/pocketbase/pocketbase/core"
	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

// HostService is the "Host" net/rpc service of one plugin process. Every
// method resolves the CallID to the principal of the host → plugin call it was
// made on behalf of. Methods never take plugin-management locks.
type HostService struct {
	server *Server
	plugin *managedPlugin

	mu    sync.Mutex
	calls map[protocol.CallID]*hostCall

	// runPlaybook executes playbooks for RunPlaybook; tests replace it.
	runPlaybook func(e *core.RequestEvent, playbooks, extraVarsFiles []string, extraVars map[string]any, tags string, verbose bool, limit string) (string, error)
}

var _ protocol.HostAPI = (*HostService)(nil)

// hostCall is one live host → plugin call. event is the live request for
// Handle calls and nil for plugin-principal calls (Initialize, RunJob, VMHook).
// session is set for an app session's call, which lives until the session
// ends; its request context is rebuilt from current records on every use.
// mu serializes host-service calls sharing the CallID because the event store
// holds per-request state.
type hostCall struct {
	mu      sync.Mutex
	ctx     context.Context
	event   *core.RequestEvent
	session *appSession
}

func newHostService(s *Server, plugin *managedPlugin) *HostService {
	return &HostService{
		server:      s,
		plugin:      plugin,
		calls:       map[protocol.CallID]*hostCall{},
		runPlaybook: s.RunAnsiblePlaybookWithVariables,
	}
}

// serve registers the host service on a fresh broker stream. The plugin dials
// it during Initialize; ServeConn returns when the plugin process exits.
func (h *HostService) serve(broker *hcplugin.MuxBroker) uint32 {
	id := broker.NextId()
	go func() {
		conn, err := broker.Accept(id)
		if err != nil {
			logger.Error(fmt.Sprintf("plugin %s did not connect to the host service: %v", h.plugin.manifest.ID, err))
			return
		}
		server := rpc.NewServer()
		if err := server.RegisterName(protocol.HostServiceName, h); err != nil {
			logger.Error(fmt.Sprintf("register host service for plugin %s: %v", h.plugin.manifest.ID, err))
			conn.Close()
			return
		}
		server.ServeConn(conn)
	}()
	return id
}

// beginRequest registers a Handle call; the returned func expires it.
func (h *HostService) beginRequest(e *core.RequestEvent) (protocol.CallID, func()) {
	return h.register(&hostCall{ctx: e.Request.Context(), event: e})
}

// beginPluginCall registers a plugin-principal call; the returned func expires it.
func (h *HostService) beginPluginCall(ctx context.Context) (protocol.CallID, func()) {
	return h.register(&hostCall{ctx: ctx})
}

// registerAppSession registers the caller call of an app session; the
// returned func expires it.
func (h *HostService) registerAppSession(session *appSession) (protocol.CallID, func()) {
	return h.register(&hostCall{ctx: session.ctx, session: session})
}

func (h *HostService) register(call *hostCall) (protocol.CallID, func()) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(fmt.Sprintf("generate plugin call ID: %v", err))
	}
	id := protocol.CallID(hex.EncodeToString(raw[:]))
	h.mu.Lock()
	h.calls[id] = call
	h.mu.Unlock()
	return id, func() {
		h.mu.Lock()
		delete(h.calls, id)
		h.mu.Unlock()
	}
}

func (h *HostService) lookup(id protocol.CallID) *hostCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls[id]
}

// isEnterprisePlugin identifies the package allowed to change license state:
// ID ludus-enterprise installed by root or by the license flow, never uploaded.
func isEnterprisePlugin(plugin *managedPlugin) bool {
	return plugin.manifest.ID == enterprisePluginID &&
		(plugin.source == protocol.SourceLicensed || plugin.source == protocol.SourceServer)
}

// hostAccess is the principal rule of a host-service method (see the HostAPI
// comments in ludus-plugin-sdk/protocol/host.go).
type hostAccess int

const (
	// accessAny: any call of any plugin.
	accessAny hostAccess = iota
	// accessCaller: a Handle call, evaluated as the caller.
	accessCaller
	// accessCallerOrSystem: a Handle call, or the plugin principal of a
	// system-level plugin.
	accessCallerOrSystem
	// accessCallerSystem: a Handle call to a system-level plugin.
	accessCallerSystem
	// accessCallerSystemAdmin: a Handle call to a system-level plugin from an admin.
	accessCallerSystemAdmin
	// accessSystem: the plugin principal of a system-level plugin, or a Handle
	// call to a system-level plugin whose caller is an admin.
	accessSystem
	// accessEnterprise: the Enterprise package only.
	accessEnterprise
)

// hostScope is the resolved principal of one host-service call.
type hostScope struct {
	h     *HostService
	ctx   context.Context
	event *core.RequestEvent // nil for plugin-principal calls
	// session is the app session of an app-session call.
	session *appSession
	// user is the effective user of a Handle call; nil for superusers.
	user      *models.User
	superuser bool
	// admin: the effective user is an admin, or the caller is a superuser.
	admin  bool
	system bool
}

func (c *hostScope) isPluginCall() bool { return c.event == nil }

// canAccessRange applies the /api/v2 rule (middleware.go): direct or group
// access, or an admin effective user. System-level plugin principals may
// address any range.
func (c *hostScope) canAccessRange(rangeNumber int) bool {
	if c.isPluginCall() {
		return c.system
	}
	if c.superuser {
		return true
	}
	if c.user == nil {
		return false
	}
	return HasRangeAccess(c.event, c.user.UserId(), rangeNumber) || c.user.IsAdmin()
}

// requireUser returns the effective user of a Handle call.
func (c *hostScope) requireUser() (*models.User, *protocol.HostError) {
	if c.user == nil {
		return nil, protocol.HostErrorf(protocol.CodeForbidden, "the request has no effective user")
	}
	return c.user, nil
}

func (c *hostScope) authorize(access hostAccess) *protocol.HostError {
	forbidden := func(format string, args ...any) *protocol.HostError {
		return protocol.HostErrorf(protocol.CodeForbidden, format, args...)
	}
	switch access {
	case accessAny:
		return nil
	case accessCaller:
		if c.isPluginCall() {
			return forbidden("this method is available only while handling a request")
		}
	case accessCallerOrSystem:
		if c.isPluginCall() && !c.system {
			return forbidden("this method requires a request or a system-level plugin")
		}
	case accessCallerSystem, accessCallerSystemAdmin:
		if c.isPluginCall() {
			return forbidden("this method is available only while handling a request")
		}
		if !c.system {
			return forbidden("this method requires a system-level plugin")
		}
		if access == accessCallerSystemAdmin && !c.admin {
			return forbidden("this method requires an admin caller")
		}
	case accessSystem:
		if !c.system {
			return forbidden("this method requires a system-level plugin")
		}
		if !c.isPluginCall() && !c.admin {
			return forbidden("this method requires an admin caller")
		}
	case accessEnterprise:
		if !isEnterprisePlugin(c.h.plugin) {
			return forbidden("this method is reserved for the licensed Ludus Enterprise package")
		}
	default:
		return forbidden("unknown access rule")
	}
	return nil
}

// run resolves the CallID, serializes calls that share it, authorizes the
// principal, and converts panics into Unavailable so a host bug never
// crashes the service.
func (h *HostService) run(id protocol.CallID, access hostAccess, method string, fn func(*hostScope) *protocol.HostError) (result *protocol.HostError) {
	call := h.lookup(id)
	if call == nil {
		return protocol.HostErrorf(protocol.CodeExpired, "call %q is unknown or has returned", id)
	}
	call.mu.Lock()
	defer call.mu.Unlock()
	if h.lookup(id) != call {
		return protocol.HostErrorf(protocol.CodeExpired, "call %q has returned", id)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.Error(fmt.Sprintf("host service %s panicked for plugin %s: %v\n%s", method, h.plugin.manifest.ID, recovered, debug.Stack()))
			result = protocol.HostErrorf(protocol.CodeUnavailable, "%s failed", method)
		}
	}()

	scope := &hostScope{
		h:       h,
		ctx:     call.ctx,
		event:   call.event,
		session: call.session,
		system:  h.plugin.manifest.Level == protocol.LevelSystem,
	}
	if call.session != nil {
		event, herr := call.session.requestEvent(call.ctx)
		if herr != nil {
			return herr
		}
		scope.event = event
	}
	if e := scope.event; e != nil {
		scope.superuser = e.Auth != nil && e.Auth.IsSuperuser()
		if user, ok := e.Get("user").(*models.User); ok && user != nil && user.Record != nil {
			scope.user = user
		}
		scope.admin = scope.superuser || (scope.user != nil && scope.user.IsAdmin())
	}
	if scope.ctx == nil {
		scope.ctx = context.Background()
	}
	if err := scope.authorize(access); err != nil {
		return err
	}
	return fn(scope)
}
