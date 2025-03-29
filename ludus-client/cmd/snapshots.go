package cmd

import (
	"encoding/json"
	"fmt"
	logger "ludus/logger"
	"ludus/rest"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var snapshotVMIDs string
var snapshotDescription string
var noRAM bool

type Snapshot struct {
	Name        string `json:"name"`
	IncludesRAM bool   `json:"includesRAM"`
	Description string `json:"description"`
	Snaptime    uint   `json:"snaptime"`
	Parent      string `json:"parent"`
	VMID        int    `json:"vmid"`
	VMName      string `json:"vmname"`
}

type ErrorInfo struct {
	VMID   int    `json:"vmid"`
	VMName string `json:"vmname"`
	Error  string `json:"error"`
}

type SnapshotListResponse struct {
	Snapshots []Snapshot  `json:"snapshots"`
	Errors    []ErrorInfo `json:"errors"`
}

type SnapshotNode struct {
	Snapshot Snapshot
	Children []*SnapshotNode
}

type SnapshotCreatePayload struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	VMIDs       []int  `json:"vmids"`
	IncludeRAM  bool   `json:"includeRAM"`
}

type SnapshotCreateResponse struct {
	SuccessArray []int       `json:"success"`
	Errors       []ErrorInfo `json:"errors"`
}

type SnapshotGenericPayload struct {
	Name  string `json:"name"`
	VMIDs []int  `json:"vmids"`
}

type SnapshotGenericResponse struct {
	SuccessArray []int       `json:"success"`
	Errors       []ErrorInfo `json:"errors"`
}

func buildHierarchicalSnapshots(flatSnapshots []Snapshot) map[int][]*SnapshotNode {
	// Group snapshots by VMID
	snapshotsByVM := make(map[int][]Snapshot)
	for _, snap := range flatSnapshots {
		snapshotsByVM[snap.VMID] = append(snapshotsByVM[snap.VMID], snap)
	}

	// Build tree for each VM
	result := make(map[int][]*SnapshotNode)
	for vmid, snapshots := range snapshotsByVM {
		// Create nodes map
		nodesMap := make(map[string]*SnapshotNode)
		for _, snap := range snapshots {
			nodesMap[snap.Name] = &SnapshotNode{
				Snapshot: snap,
				Children: make([]*SnapshotNode, 0),
			}
		}

		// Build relationships
		var roots []*SnapshotNode
		for _, node := range nodesMap {
			if node.Snapshot.Parent == "" {
				roots = append(roots, node)
			} else {
				parent := nodesMap[node.Snapshot.Parent]
				parent.Children = append(parent.Children, node)
			}
		}

		result[vmid] = roots
	}

	return result
}

func formatHierarchicalSnapshotsResponse(hierarchicalSnapshots map[int][]*SnapshotNode) {
	for vmid, roots := range hierarchicalSnapshots {
		if len(roots) == 0 {
			continue
		}

		// Print VM header using the first snapshot's VM name
		fmt.Printf("\nVM %d (%s)\n", vmid, roots[0].Snapshot.VMName)

		// Print each root and its children
		for i, root := range roots {
			isLast := i == len(roots)-1
			printSnapshotNode(root, "", isLast)
		}
	}
}

func printSnapshotNode(node *SnapshotNode, prefix string, isLast bool) {
	// Choose the connector based on whether this is the last sibling
	connector := "├── "
	if isLast {
		connector = "└── "
	}

	// Print this node
	snapTime := ""
	if node.Snapshot.Snaptime > 0 {
		snapTime = time.Unix(int64(node.Snapshot.Snaptime), 0).Format("2006-01-02 15:04:05")
	}

	description := ""
	if node.Snapshot.Description != "" {
		description = fmt.Sprintf(" (%s)", node.Snapshot.Description)
	}

	ramString := ""
	if node.Snapshot.IncludesRAM {
		ramString = " [includes RAM]"
	}

	fmt.Printf("%s%s%s %s%s%s\n", prefix, connector, node.Snapshot.Name, snapTime, description, ramString)

	// Prepare the prefix for children
	childPrefix := prefix
	if isLast {
		childPrefix += "    "
	} else {
		childPrefix += "│   "
	}

	// Print all children
	for i, child := range node.Children {
		isLastChild := i == len(node.Children)-1
		printSnapshotNode(child, childPrefix, isLastChild)
	}
}

var snapshotsCmd = &cobra.Command{
	Use:     "snapshots",
	Short:   "Manage snapshots for VMs",
	Long:    ``,
	Aliases: []string{"snapshot"},
}

var snapshotsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List snapshots for VMs",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		// Build the API path with query parameters if snapshotVMIDs is specified
		apiPath := "/snapshots/list"
		if userID != "" && snapshotVMIDs != "" {
			apiPath = fmt.Sprintf("%s?userID=%s&vmids=%s", apiPath, userID, snapshotVMIDs)
		} else if userID != "" {
			apiPath = fmt.Sprintf("%s?userID=%s", apiPath, userID)
		} else if snapshotVMIDs != "" {
			apiPath = fmt.Sprintf("%s?vmids=%s", apiPath, snapshotVMIDs)
		}

		responseJSON, success := rest.GenericGet(client, apiPath)
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		var snapshotResponse SnapshotListResponse
		err := json.Unmarshal(responseJSON, &snapshotResponse)
		if err != nil {
			logger.Logger.Fatalf("Failed to unmarshal snapshot data: %v", err)
			return
		}
		if len(snapshotResponse.Errors) > 0 {
			for _, error := range snapshotResponse.Errors {
				logger.Logger.Errorf("%s", error.Error)
			}
		}

		hierarchicalSnapshots := buildHierarchicalSnapshots(snapshotResponse.Snapshots)
		formatHierarchicalSnapshotsResponse(hierarchicalSnapshots)
	},
}

func setupSnapshotsListCmd(command *cobra.Command) {
	command.Flags().StringVarP(&snapshotVMIDs, "vmids", "n", "", "A VM ID (104) or multiple VM IDs or names (104,105) to list snapshots for (default: all VMs in the range)")
}

var snapshotsCreateCmd = &cobra.Command{
	Use:     "create [snapshot name]",
	Short:   "Create a snapshot for VMs",
	Args:    cobra.ExactArgs(1),
	Aliases: []string{"take"},
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		apiPath := "/snapshots/create"
		if userID != "" {
			apiPath = fmt.Sprintf("%s?userID=%s", apiPath, userID)
		}

		var snapshotVMIDsIntArray []int
		if snapshotVMIDs != "" {
			snapshotVMIDsArray := strings.Split(snapshotVMIDs, ",")
			for _, vmid := range snapshotVMIDsArray {
				snapshotVMIDsInt, err := strconv.Atoi(vmid)
				if err != nil {
					logger.Logger.Fatalf("Failed to convert VM ID to int: %v", err)
					return
				}
				snapshotVMIDsIntArray = append(snapshotVMIDsIntArray, snapshotVMIDsInt)
			}
		}

		snapshotCreatePayload := SnapshotCreatePayload{
			Name:        args[0],
			Description: snapshotDescription,
			VMIDs:       snapshotVMIDsIntArray,
			IncludeRAM:  !noRAM,
		}

		responseJSON, success := rest.GenericJSONPost(client, apiPath, snapshotCreatePayload)
		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		var snapshotCreateResponse SnapshotCreateResponse
		err := json.Unmarshal(responseJSON, &snapshotCreateResponse)
		if err != nil {
			logger.Logger.Fatalf("Failed to unmarshal snapshot data: %v", err)
			return
		}

		if len(snapshotCreateResponse.Errors) > 0 {
			for _, error := range snapshotCreateResponse.Errors {
				logger.Logger.Errorf("Error creating snapshot for VM %d: %s", error.VMID, error.Error)
			}
		}

		if len(snapshotCreateResponse.SuccessArray) > 0 {
			for _, success := range snapshotCreateResponse.SuccessArray {
				logger.Logger.Infof("Successfully created snapshot '%s' for VM %d", args[0], success)
			}
		}

	},
}

func setupSnapshotsCreateCmd(command *cobra.Command) {
	command.Flags().StringVarP(&snapshotVMIDs, "vmids", "n", "", "A VM ID (104) or multiple VM IDs (104,105) to create snapshots for (default: all VMs in the range)")
	command.Flags().StringVarP(&snapshotDescription, "description", "d", "", "Description of the snapshot")
	command.Flags().BoolVarP(&noRAM, "noRAM", "r", false, "Don't include RAM in the snapshot")
}

var snapshotsRollbackCmd = &cobra.Command{
	Use:     "revert [snapshot name]",
	Short:   "revert VM(s) to a snapshot",
	Args:    cobra.ExactArgs(1),
	Aliases: []string{"rollback"},
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		apiPath := "/snapshots/rollback"
		if userID != "" {
			apiPath = fmt.Sprintf("%s?userID=%s", apiPath, userID)
		}

		var snapshotVMIDsIntArray []int
		if snapshotVMIDs != "" {
			snapshotVMIDsArray := strings.Split(snapshotVMIDs, ",")
			for _, vmid := range snapshotVMIDsArray {
				snapshotVMIDsInt, err := strconv.Atoi(vmid)
				if err != nil {
					logger.Logger.Fatalf("Failed to convert VM ID to int: %v", err)
					return
				}
				snapshotVMIDsIntArray = append(snapshotVMIDsIntArray, snapshotVMIDsInt)
			}
		}

		snapshotRollbackPayload := SnapshotGenericPayload{
			Name:  args[0],
			VMIDs: snapshotVMIDsIntArray,
		}

		responseJSON, success := rest.GenericJSONPost(client, apiPath, snapshotRollbackPayload)
		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		var snapshotRollbackResponse SnapshotGenericResponse
		err := json.Unmarshal(responseJSON, &snapshotRollbackResponse)
		if err != nil {
			logger.Logger.Fatalf("Failed to unmarshal snapshot data: %v", err)
			return
		}

		if len(snapshotRollbackResponse.Errors) > 0 {
			for _, error := range snapshotRollbackResponse.Errors {
				logger.Logger.Errorf("Error rolling back VM %d to snapshot '%s': %s", error.VMID, args[0], error.Error)
			}
		}

		if len(snapshotRollbackResponse.SuccessArray) > 0 {
			for _, success := range snapshotRollbackResponse.SuccessArray {
				logger.Logger.Infof("Successfully rolled back VM %d to snapshot '%s'", success, args[0])
			}
		}
	},
}

func setupSnapshotsRollbackCmd(command *cobra.Command) {
	command.Flags().StringVarP(&snapshotVMIDs, "vmids", "n", "", "A VM ID (104) or multiple VM IDs (104,105) to rollback snapshots for (default: all VMs in the range)")
}

var snapshotRemoveCmd = &cobra.Command{
	Use:     "rm [snapshot name]",
	Short:   "rm a snapshot",
	Args:    cobra.ExactArgs(1),
	Aliases: []string{"delete", "remove"},
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		apiPath := "/snapshots/remove"
		if userID != "" {
			apiPath = fmt.Sprintf("%s?userID=%s", apiPath, userID)
		}

		var snapshotVMIDsIntArray []int
		if snapshotVMIDs != "" {
			snapshotVMIDsArray := strings.Split(snapshotVMIDs, ",")
			for _, vmid := range snapshotVMIDsArray {
				snapshotVMIDsInt, err := strconv.Atoi(vmid)
				if err != nil {
					logger.Logger.Fatalf("Failed to convert VM ID to int: %v", err)
					return
				}
				snapshotVMIDsIntArray = append(snapshotVMIDsIntArray, snapshotVMIDsInt)
			}
		}

		snapshotRemovePayload := SnapshotGenericPayload{
			Name:  args[0],
			VMIDs: snapshotVMIDsIntArray,
		}

		responseJSON, success := rest.GenericJSONPost(client, apiPath, snapshotRemovePayload)
		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		var snapshotRemoveResponse SnapshotGenericResponse
		err := json.Unmarshal(responseJSON, &snapshotRemoveResponse)
		if err != nil {
			logger.Logger.Fatalf("Failed to unmarshal snapshot data: %v", err)
			return
		}

		if len(snapshotRemoveResponse.Errors) > 0 {
			for _, error := range snapshotRemoveResponse.Errors {
				logger.Logger.Errorf("Error removing snapshot '%s' from VM %d: %s", args[0], error.VMID, error.Error)
			}
		}

		if len(snapshotRemoveResponse.SuccessArray) > 0 {
			for _, success := range snapshotRemoveResponse.SuccessArray {
				logger.Logger.Infof("Successfully removed snapshot '%s' from VM %d", args[0], success)
			}
		}

	},
}

func setupSnapshotRemoveCmd(command *cobra.Command) {
	command.Flags().StringVarP(&snapshotVMIDs, "vmids", "n", "", "A VM ID (104) or multiple VM IDs (104,105) to remove snapshots from (default: all VMs in the range)")
}

func init() {
	snapshotsCmd.AddCommand(snapshotsListCmd)
	setupSnapshotsListCmd(snapshotsListCmd)
	snapshotsCmd.AddCommand(snapshotsCreateCmd)
	setupSnapshotsCreateCmd(snapshotsCreateCmd)
	snapshotsCmd.AddCommand(snapshotsRollbackCmd)
	setupSnapshotsRollbackCmd(snapshotsRollbackCmd)
	snapshotsCmd.AddCommand(snapshotRemoveCmd)
	setupSnapshotRemoveCmd(snapshotRemoveCmd)
	rootCmd.AddCommand(snapshotsCmd)
}
