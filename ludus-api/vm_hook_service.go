package ludusapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/user"
	"regexp"
	"strconv"
	"sync"
	"syscall"
	"time"

	goproxmox "github.com/luthermonson/go-proxmox"
)

const (
	vmHookServiceDirectory = "/run/ludus"
	VMHookServiceSocket    = vmHookServiceDirectory + "/vm-hooks.sock"
)

var (
	vmHookServiceMu       sync.Mutex
	vmHookServiceServer   *http.Server
	vmHookServiceListener net.Listener
	vmHookRangeIDPattern  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*(?:/[A-Za-z0-9_-]+){0,2}$`)
	vmHookNamePattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
)

type vmHookServiceRequest struct {
	RangeID   string            `json:"rangeId"`
	VMID      vmHookServiceVMID `json:"vmId"`
	VMName    string            `json:"vmName"`
	FirstBoot vmHookServiceBool `json:"firstBoot,omitempty"`
}

type vmHookServiceVMID int

type vmHookServiceBool bool

func (value *vmHookServiceBool) UnmarshalJSON(data []byte) error {
	encoded := string(bytes.TrimSpace(data))
	if len(encoded) > 1 && encoded[0] == '"' {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		parsed, err := strconv.ParseBool(text)
		if err != nil {
			return fmt.Errorf("firstBoot must be a boolean: %w", err)
		}
		*value = vmHookServiceBool(parsed)
		return nil
	}
	var parsed bool
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}
	*value = vmHookServiceBool(parsed)
	return nil
}

func (value *vmHookServiceVMID) UnmarshalJSON(data []byte) error {
	encoded := string(bytes.TrimSpace(data))
	if len(encoded) > 1 && encoded[0] == '"' {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		parsed, err := strconv.Atoi(text)
		if err != nil {
			return fmt.Errorf("vmId must be a decimal integer: %w", err)
		}
		*value = vmHookServiceVMID(parsed)
		return nil
	}
	var parsed int
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}
	*value = vmHookServiceVMID(parsed)
	return nil
}

type vmHookServiceResponse struct {
	Handled bool     `json:"handled"`
	Ready   bool     `json:"ready,omitempty"`
	Address string   `json:"address,omitempty"`
	Status  VMStatus `json:"status,omitempty"`
	Error   string   `json:"error,omitempty"`
}

func validateVMHookServiceDirectory() error {
	info, err := os.Lstat(vmHookServiceDirectory)
	if os.IsNotExist(err) {
		return os.Mkdir(vmHookServiceDirectory, 0755)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("VM hook service path %s is not a real directory", vmHookServiceDirectory)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 {
		return fmt.Errorf("VM hook service directory %s is not owned by root", vmHookServiceDirectory)
	}
	if info.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("VM hook service directory %s is group/world writable", vmHookServiceDirectory)
	}
	return nil
}

func removeStaleVMHookSocket() error {
	info, err := os.Lstat(VMHookServiceSocket)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("VM hook service path %s exists and is not a socket", VMHookServiceSocket)
	}
	return os.Remove(VMHookServiceSocket)
}

// StartVMHookService exposes root-owned lifecycle hooks to local deployment
// automation and to the unprivileged Ludus status path. The socket is created
// only when at least one loaded plugin implements a VM lifecycle hook.
func (s *Server) StartVMHookService() error {
	if os.Geteuid() != 0 {
		return nil
	}

	vmHookServiceMu.Lock()
	defer vmHookServiceMu.Unlock()
	if vmHookServiceServer != nil {
		return nil
	}
	if !s.hasVMLifecycleHooks() {
		// A removed plugin may leave a stale socket after an unclean shutdown.
		// Clean it up when safe, but never require hook infrastructure to start
		// a server that has no lifecycle hooks.
		if _, err := os.Lstat(vmHookServiceDirectory); err == nil {
			if err := validateVMHookServiceDirectory(); err == nil {
				_ = removeStaleVMHookSocket()
			}
		}
		return nil
	}
	if err := validateVMHookServiceDirectory(); err != nil {
		return err
	}
	if err := removeStaleVMHookSocket(); err != nil {
		return err
	}
	listener, err := net.Listen("unix", VMHookServiceSocket)
	if err != nil {
		return err
	}
	cleanupListener := func() {
		_ = listener.Close()
		_ = os.Remove(VMHookServiceSocket)
	}
	group, err := user.LookupGroup("ludus")
	if err != nil {
		cleanupListener()
		return fmt.Errorf("lookup ludus group: %w", err)
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil {
		cleanupListener()
		return fmt.Errorf("parse ludus group ID: %w", err)
	}
	if err := os.Chown(VMHookServiceSocket, 0, gid); err != nil {
		cleanupListener()
		return err
	}
	if err := os.Chmod(VMHookServiceSocket, 0660); err != nil {
		cleanupListener()
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/start", s.handleVMHookServiceStart)
	mux.HandleFunc("/v1/status", s.handleVMHookServiceStatus)
	mux.HandleFunc("/v1/statuses", s.handleVMHookServiceStatuses)
	mux.HandleFunc("/v1/address", s.handleVMHookServiceAddress)
	service := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      25 * time.Minute,
		IdleTimeout:       30 * time.Second,
	}
	vmHookServiceServer = service
	vmHookServiceListener = listener
	go func() {
		if err := service.Serve(listener); err != nil && err != http.ErrServerClosed {
			logger.Error(fmt.Sprintf("VM hook service stopped: %v", err))
		}
	}()
	return nil
}

func (s *Server) StopVMHookService() error {
	vmHookServiceMu.Lock()
	defer vmHookServiceMu.Unlock()
	if vmHookServiceServer == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := vmHookServiceServer.Shutdown(ctx)
	if vmHookServiceListener != nil {
		_ = vmHookServiceListener.Close()
	}
	_ = os.Remove(VMHookServiceSocket)
	vmHookServiceServer = nil
	vmHookServiceListener = nil
	return err
}

func decodeVMHookServiceRequest(response http.ResponseWriter, request *http.Request) (vmHookServiceRequest, bool) {
	if request.Method != http.MethodPost {
		writeVMHookServiceResponse(response, http.StatusMethodNotAllowed, vmHookServiceResponse{Error: "POST is required"})
		return vmHookServiceRequest{}, false
	}
	request.Body = http.MaxBytesReader(response, request.Body, 16*1024)
	var body vmHookServiceRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeVMHookServiceResponse(response, http.StatusBadRequest, vmHookServiceResponse{Error: "invalid request body: " + err.Error()})
		return vmHookServiceRequest{}, false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeVMHookServiceResponse(response, http.StatusBadRequest, vmHookServiceResponse{Error: "request contains trailing data"})
		return vmHookServiceRequest{}, false
	}
	if !vmHookRangeIDPattern.MatchString(body.RangeID) || (body.VMID != 0 && body.VMID < 100) || !vmHookNamePattern.MatchString(body.VMName) {
		writeVMHookServiceResponse(response, http.StatusBadRequest, vmHookServiceResponse{Error: "rangeId and vmName are required; vmId must be a valid VMID when supplied"})
		return vmHookServiceRequest{}, false
	}
	return body, true
}

func writeVMHookServiceResponse(response http.ResponseWriter, status int, body vmHookServiceResponse) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(body)
}

func (s *Server) verifiedVMHookRequest(ctx context.Context, body vmHookServiceRequest, source string) (VMHookRequest, error) {
	client, err := GetRootGoProxmoxClient()
	if err != nil {
		return VMHookRequest{}, err
	}
	attempts := 1
	if source == StartVMSourceDeploymentFirstBoot {
		// Proxmox's cluster listing can briefly retain the source template's
		// name or pool after cloning. Wait for the verified identity to appear;
		// never dispatch a hook using mismatched cluster metadata.
		attempts = 10
	}
	return verifyVMHookRequestWithRetry(ctx, attempts, time.Second, func() (VMHookRequest, error) {
		return verifiedVMHookResource(ctx, client, body, source)
	})
}

func verifyVMHookRequestWithRetry(ctx context.Context, attempts int, delay time.Duration, verify func() (VMHookRequest, error)) (VMHookRequest, error) {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return VMHookRequest{}, err
		}
		request, err := verify()
		if err == nil || attempt+1 >= attempts {
			return request, err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return VMHookRequest{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func verifiedVMHookResource(ctx context.Context, client *goproxmox.Client, body vmHookServiceRequest, source string) (VMHookRequest, error) {
	var err error
	vmID := int(body.VMID)
	var resource *goproxmox.ClusterResource
	if vmID == 0 {
		resource, err = getVMResourceByIdentity(ctx, client, body.RangeID, body.VMName)
		if err == nil {
			vmID = int(resource.VMID)
		}
	} else {
		resource, err = getVMResource(ctx, client, vmID)
	}
	if err != nil {
		return VMHookRequest{}, err
	}
	if resource.Type != "qemu" || resource.Template == 1 {
		return VMHookRequest{}, fmt.Errorf("VMID %d is not a QEMU range VM", vmID)
	}
	if resource.Name != body.VMName {
		return VMHookRequest{}, fmt.Errorf("VMID %d name does not match %q", vmID, body.VMName)
	}
	if resource.Pool != body.RangeID {
		return VMHookRequest{}, fmt.Errorf("VMID %d belongs to pool %q, not range %q", vmID, resource.Pool, body.RangeID)
	}
	return VMHookRequest{
		Source:  source,
		RangeID: body.RangeID,
		VMID:    vmID,
		VMName:  resource.Name,
		Node:    resource.Node,
		Pool:    resource.Pool,
		Status:  resource.Status,
	}, nil
}

func (s *Server) handleVMHookServiceStart(response http.ResponseWriter, request *http.Request) {
	body, ok := decodeVMHookServiceRequest(response, request)
	if !ok {
		return
	}
	source := StartVMSourceDeployment
	if bool(body.FirstBoot) {
		source = StartVMSourceDeploymentFirstBoot
	}
	hookRequest, err := s.verifiedVMHookRequest(request.Context(), body, source)
	if err != nil {
		writeVMHookServiceResponse(response, http.StatusConflict, vmHookServiceResponse{Error: err.Error()})
		return
	}
	handled, err := s.runStartVMHooks(request.Context(), hookRequest)
	if err != nil {
		writeVMHookServiceResponse(response, http.StatusConflict, vmHookServiceResponse{Error: err.Error()})
		return
	}
	writeVMHookServiceResponse(response, http.StatusOK, vmHookServiceResponse{Handled: handled})
}

func (s *Server) handleVMHookServiceStatus(response http.ResponseWriter, request *http.Request) {
	body, ok := decodeVMHookServiceRequest(response, request)
	if !ok {
		return
	}
	hookRequest, err := s.verifiedVMHookRequest(request.Context(), body, StartVMSourceAPI)
	if err != nil {
		writeVMHookServiceResponse(response, http.StatusConflict, vmHookServiceResponse{Error: err.Error()})
		return
	}
	status, handled, err := s.runVMStatusHooks(request.Context(), hookRequest)
	if err != nil {
		writeVMHookServiceResponse(response, http.StatusConflict, vmHookServiceResponse{Error: err.Error()})
		return
	}
	writeVMHookServiceResponse(response, http.StatusOK, vmHookServiceResponse{Handled: handled, Status: status})
}

func (s *Server) resolveVMStatus(ctx context.Context, request VMHookRequest) (VMStatus, bool, error) {
	statuses, err := s.resolveVMStatuses(ctx, []VMHookRequest{request})
	status, handled := statuses[request.VMID]
	return status, handled, err
}

func getVMResource(ctx context.Context, client *goproxmox.Client, vmID int) (*goproxmox.ClusterResource, error) {
	cluster, err := client.Cluster(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster client: %w", err)
	}
	resources, err := cluster.Resources(ctx, "vm")
	if err != nil {
		return nil, fmt.Errorf("failed to list VMs in the cluster: %w", err)
	}
	for _, resource := range resources {
		if int(resource.VMID) == vmID {
			return resource, nil
		}
	}
	return nil, fmt.Errorf("VMID %d not found in the cluster", vmID)
}

func getVMResourceByIdentity(ctx context.Context, client *goproxmox.Client, rangeID, vmName string) (*goproxmox.ClusterResource, error) {
	cluster, err := client.Cluster(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster client: %w", err)
	}
	resources, err := cluster.Resources(ctx, "vm")
	if err != nil {
		return nil, fmt.Errorf("failed to list VMs in the cluster: %w", err)
	}
	for _, resource := range resources {
		if resource.Pool == rangeID && resource.Name == vmName {
			return resource, nil
		}
	}
	return nil, fmt.Errorf("VM %q was not found in range %q", vmName, rangeID)
}
