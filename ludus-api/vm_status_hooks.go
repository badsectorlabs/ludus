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

	goproxmox "github.com/luthermonson/go-proxmox"
)

type vmStatusBatchResponse struct {
	Statuses map[int]VMStatus `json:"statuses,omitempty"`
	Error    string           `json:"error,omitempty"`
}

func (s *Server) selectVMStatusRequests(ctx context.Context, requests []VMHookRequest) ([]VMHookRequest, error) {
	var selected []VMHookRequest
	for _, request := range requests {
		for _, plugin := range s.pluginSnapshot() {
			if !plugin.metadata.VMHooks.Status {
				continue
			}
			applies, err := pluginSelectsVM(ctx, plugin, request)
			if err != nil {
				return nil, err
			}
			if applies {
				selected = append(selected, request)
				break
			}
		}
	}
	return selected, nil
}

// Ordinary VMs require neither the admin service nor a second cluster lookup.
// Selected VMs use one RPC and one verified cluster snapshot per range refresh.
func (s *Server) resolveVMStatuses(ctx context.Context, requests []VMHookRequest) (map[int]VMStatus, error) {
	selected, err := s.selectVMStatusRequests(ctx, requests)
	if err != nil || len(selected) == 0 {
		return nil, err
	}
	if os.Geteuid() == 0 {
		return s.runVMStatusRequests(ctx, selected)
	}
	return callVMHookStatusBatch(ctx, selected)
}

func (s *Server) runVMStatusRequests(ctx context.Context, requests []VMHookRequest) (map[int]VMStatus, error) {
	statuses := make(map[int]VMStatus)
	for _, request := range requests {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		status, handled, err := s.runVMStatusHooks(ctx, request)
		if err != nil {
			return nil, err
		}
		if handled {
			statuses[request.VMID] = status
		}
	}
	return statuses, nil
}

func (s *Server) verifiedVMStatusBatch(ctx context.Context, bodies []vmHookServiceRequest, load func(context.Context) ([]*goproxmox.ClusterResource, error)) (map[int]VMStatus, error) {
	resources, err := load(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[int]*goproxmox.ClusterResource, len(resources))
	for _, resource := range resources {
		byID[int(resource.VMID)] = resource
	}
	requests := make([]VMHookRequest, 0, len(bodies))
	seen := make(map[int]bool)
	for _, body := range bodies {
		id := int(body.VMID)
		resource := byID[id]
		if id < 100 || seen[id] || !vmHookRangeIDPattern.MatchString(body.RangeID) || !vmHookNamePattern.MatchString(body.VMName) {
			return nil, fmt.Errorf("invalid or duplicate VM identity in status batch")
		}
		if resource == nil || resource.Type != "qemu" || resource.Template == 1 || resource.Name != body.VMName || resource.Pool != body.RangeID {
			return nil, fmt.Errorf("VMID %d does not match the requested range VM", id)
		}
		seen[id] = true
		requests = append(requests, VMHookRequest{Source: StartVMSourceAPI, RangeID: resource.Pool, VMID: id, VMName: resource.Name, Node: resource.Node, Pool: resource.Pool, Status: resource.Status})
	}
	return s.runVMStatusRequests(ctx, requests)
}

func (s *Server) handleVMHookServiceStatuses(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeVMHookServiceResponse(response, http.StatusMethodNotAllowed, vmHookServiceResponse{Error: "POST is required"})
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1024*1024))
	decoder.DisallowUnknownFields()
	var bodies []vmHookServiceRequest
	if err := decoder.Decode(&bodies); err != nil || len(bodies) == 0 || len(bodies) > 4096 {
		writeVMHookServiceResponse(response, http.StatusBadRequest, vmHookServiceResponse{Error: "invalid VM status batch"})
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeVMHookServiceResponse(response, http.StatusBadRequest, vmHookServiceResponse{Error: "request contains trailing data"})
		return
	}
	statuses, err := s.verifiedVMStatusBatch(request.Context(), bodies, func(ctx context.Context) ([]*goproxmox.ClusterResource, error) {
		client, err := GetRootGoProxmoxClient()
		if err != nil {
			return nil, err
		}
		cluster, err := client.Cluster(ctx)
		if err != nil {
			return nil, err
		}
		return cluster.Resources(ctx, "vm")
	})
	if err != nil {
		writeVMHookServiceResponse(response, http.StatusConflict, vmHookServiceResponse{Error: err.Error()})
		return
	}
	response.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(response).Encode(vmStatusBatchResponse{Statuses: statuses})
}

func callVMHookStatusBatch(ctx context.Context, requests []VMHookRequest) (map[int]VMStatus, error) {
	bodies := make([]vmHookServiceRequest, 0, len(requests))
	for _, request := range requests {
		bodies = append(bodies, vmHookServiceRequest{RangeID: request.RangeID, VMID: vmHookServiceVMID(request.VMID), VMName: request.VMName})
	}
	body, err := json.Marshal(bodies)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", VMHookServiceSocket)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix/v1/statuses", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("call root VM status service: %w", err)
	}
	defer response.Body.Close()
	var result vmStatusBatchResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 1024*1024)).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode root VM status response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("root VM status service rejected request: %s (%s)", result.Error, response.Status)
	}
	return result.Statuses, nil
}
