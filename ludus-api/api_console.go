package ludusapi

import (
	"context"
	"fmt"
	"ludusapi/dto"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/luthermonson/go-proxmox"
	"github.com/pocketbase/pocketbase/core"
)

// TicketStore to hold VNC proxy details temporarily
var ticketStore sync.Map

var upgrader = websocket.Upgrader{
	CheckOrigin:     func(r *http.Request) bool { return true },
	Subprotocols:    []string{"binary"},
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

// Generates a one-time VNC ticket for a specific VM if the calling user has access
func getConsoleWebsocketTicket(e *core.RequestEvent) error {

	proxmoxClient, err := GetGoProxmoxClientForUserUsingToken(e)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	vmidStr := e.Request.URL.Query().Get("VMID")
	vmid, err := strconv.Atoi(vmidStr)
	if err != nil {
		return JSONError(e, http.StatusBadRequest, "Invalid VMID")
	}

	nodeName, err := findNodeForVM(ctx, proxmoxClient, uint64(vmid))
	if err != nil {
		logger.Error(fmt.Sprintf("[Websocket] Find Node Error: %v", err))
		return JSONError(e, http.StatusNotFound, fmt.Sprintf("Could not locate VM: %v", err))
	}

	node, err := proxmoxClient.Node(ctx, nodeName)
	if err != nil {
		logger.Error(fmt.Sprintf("[Websocket] Get Node Error: %v", err))
		return JSONError(e, http.StatusInternalServerError, "Failed to access node")
	}

	vm, err := node.VirtualMachine(ctx, vmid)
	if err != nil {
		logger.Error(fmt.Sprintf("[Websocket] Get VM Error: %v", err))
		return JSONError(e, http.StatusInternalServerError, "Failed to access VM")
	}

	vncProxy, err := vm.VNCProxy(ctx, &proxmox.VNCConfig{
		Websocket:        true,
		GeneratePassword: true,
	})
	if err != nil {
		logger.Error(fmt.Sprintf("[Websocket] VNC Ticket Error: %v", err))
		return JSONError(e, http.StatusInternalServerError, "Failed to create VNC ticket")
	}

	// Store the proxy session keyed by the ticket
	ticketStore.Store(vncProxy.Ticket, map[string]interface{}{
		"vncProxy": vncProxy,
		"vm":       vm,
	})

	resp := dto.GetConsoleWebsocketTicketResponse{
		Ticket:   vncProxy.Ticket,
		Password: vncProxy.Password,
		Port:     int(vncProxy.Port),
	}

	return e.JSON(http.StatusOK, resp)
}

// Upgrades the connection to a WebSocket for VNC streaming. Requires a valid ticket obtained from /vm/console/ticket
func vmConsoleWebsocketHandler(e *core.RequestEvent) error {

	logger.Debug("[Websocket] vmConsoleWebsocketHandler called")
	ticket := e.Request.URL.Query().Get("ticket")
	if ticket == "" {
		return JSONError(e, http.StatusBadRequest, "Missing ticket")
	}

	val, ok := ticketStore.Load(ticket)
	if !ok {
		return JSONError(e, http.StatusForbidden, "Invalid or expired ticket")
	}

	logger.Debug("[Websocket] ticket found in ticketStore")

	// Clean up ticket (one-time use)
	ticketStore.Delete(ticket)

	data := val.(map[string]interface{})
	vncProxy := data["vncProxy"].(*proxmox.VNC)
	vm := data["vm"].(*proxmox.VirtualMachine)

	logger.Info(fmt.Sprintf("[Websocket] Using cached VNC Proxy for ticket: %s...", ticket[:10]))

	// Connect to Proxmox VNC Stream
	send, recv, errs, close, err := vm.VNCWebSocket(vncProxy)
	if err != nil {
		logger.Error(fmt.Sprintf("[Websocket] VNC Connection Error: %v", err))
		return JSONError(e, http.StatusBadGateway, "Failed to connect to Hypervisor")
	}
	defer close()

	logger.Info("[Websocket] Connected to Proxmox VNC. Channels initialized.")

	// Upgrade Client Connection to WebSocket
	logger.Info(fmt.Sprintf("[Frontend] Client requested subprotocols: %s", e.Request.Header.Get("Sec-WebSocket-Protocol")))
	ws, err := upgrader.Upgrade(e.Response, e.Request, nil)
	if err != nil {
		logger.Error(fmt.Sprintf("[Frontend] Upgrade Error: %v", err))
		return JSONError(e, http.StatusInternalServerError, "Failed to upgrade connection to WebSocket")
	}
	logger.Info(fmt.Sprintf("[Frontend] Websocket Upgraded. Subprotocol: %s", ws.Subprotocol()))
	defer ws.Close()

	// Bridge the Traffic

	done := make(chan struct{})

	go func() {
		for {
			select {
			case <-done:
				return
			case msg := <-recv:
				// log.Debug().Bytes("msg", msg).Msg("proxmox:")
				err = ws.WriteMessage(websocket.BinaryMessage, msg)
				if err != nil {
					done <- struct{}{}
					logger.Error(fmt.Sprintf("[Websocket] Failed to write to browser: %v", err))
					return
				}
			}
		}
	}()

	go func() {
		for {
			select {
			case <-done:
				return
			case err := <-errs:
				if err != nil {
					logger.Error(fmt.Sprintf("[Websocket] Proxmox WebSocket Error: %v", err))
				}
				done <- struct{}{}
				return
			default:
				_, msg, err := ws.ReadMessage()
				if err != nil {
					done <- struct{}{}
					if strings.Contains(err.Error(), "use of closed network connection") {
						return
					}
					if !websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
						return
					}
					logger.Error(fmt.Sprintf("[Websocket] Error reading from websocket: %v", err))
					return
				}
				// log.Debug().Bytes("msg", msg).Msg("Client:")
				send <- msg
			}
		}
	}()

	<-done

	logger.Debug("[Websocket] returning from vmConsoleWebsocketHandler")

	return nil
}
