package ludusapi

import (
	"database/sql"
	"fmt"
	"io"
	"ludusapi/dto"
	"ludusapi/models"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
)

var blueprintIDRegex = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_\-]*(\/[A-Za-z0-9_\-]+){0,2}$`)

func normalizeBlueprintID(blueprintID string) string {
	return strings.TrimSpace(blueprintID)
}

func getBlueprintPublicID(blueprintRecord *core.Record) string {
	blueprintID := normalizeBlueprintID(blueprintRecord.GetString("blueprintID"))
	if blueprintID == "" {
		return blueprintRecord.Id
	}
	return blueprintID
}

func blueprintThumbnailURL(blueprintRecord *core.Record) string {
	if blueprintRecord == nil {
		return ""
	}

	thumbnail := blueprintRecord.GetString("thumbnail")
	if thumbnail == "" {
		return ""
	}

	return fmt.Sprintf("/api/files/blueprints/%s/%s", blueprintRecord.Id, url.PathEscape(thumbnail))
}

func validateBlueprintID(blueprintID string) error {
	if blueprintID == "" {
		return fmt.Errorf("Blueprint ID is required")
	}
	if !blueprintIDRegex.MatchString(blueprintID) {
		return fmt.Errorf("Blueprint ID must be a valid ID (e.g. 'blueprint-1', 'team/windows' or 'my_blueprint')")
	}
	return nil
}

func blueprintIDExists(e *core.RequestEvent, blueprintID string) (bool, error) {
	existingBlueprintRecord, err := e.App.FindFirstRecordByData("blueprints", "blueprintID", blueprintID)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return existingBlueprintRecord != nil, nil
}

func getNextCopyBlueprintID(e *core.RequestEvent, sourceBlueprintID string) (string, error) {
	baseBlueprintID := normalizeBlueprintID(sourceBlueprintID)
	if !blueprintIDRegex.MatchString(baseBlueprintID) {
		baseBlueprintID = "blueprint"
	}

	candidateBlueprintID := fmt.Sprintf("%s-copy", baseBlueprintID)
	for copyNumber := 2; ; copyNumber++ {
		exists, err := blueprintIDExists(e, candidateBlueprintID)
		if err != nil {
			return "", err
		}
		if !exists {
			return candidateBlueprintID, nil
		}
		candidateBlueprintID = fmt.Sprintf("%s-copy-%d", baseBlueprintID, copyNumber)
	}
}

func getBlueprintRecordFromRequest(e *core.RequestEvent) (*core.Record, error) {
	blueprintID := normalizeBlueprintID(e.Request.PathValue("blueprintID"))
	if blueprintID == "" {
		return nil, JSONError(e, http.StatusBadRequest, "Blueprint ID is required")
	}

	blueprintRecord, err := e.App.FindFirstRecordByData("blueprints", "blueprintID", blueprintID)
	if err == nil {
		return blueprintRecord, nil
	}
	if err != sql.ErrNoRows {
		return nil, JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error finding blueprint %s: %v", blueprintID, err))
	}

	// Backward compatibility for existing blueprints created before blueprintID existed.
	blueprintRecord, err = e.App.FindRecordById("blueprints", blueprintID)
	if err != nil {
		return nil, JSONError(e, http.StatusNotFound, fmt.Sprintf("Blueprint %s not found", blueprintID))
	}

	return blueprintRecord, nil
}

func userCanAccessBlueprint(e *core.RequestEvent, user *models.User, blueprintRecord *core.Record) (bool, error) {
	if user.IsAdmin() {
		return true, nil
	}

	if blueprintRecord.GetString("owner") == user.Id {
		return true, nil
	}

	if slices.Contains(blueprintRecord.GetStringSlice("sharedUsers"), user.Id) {
		return true, nil
	}

	for _, groupID := range blueprintRecord.GetStringSlice("sharedGroups") {
		groupRecord, err := e.App.FindRecordById("groups", groupID)
		if err != nil {
			continue
		}

		if slices.Contains(groupRecord.GetStringSlice("members"), user.Id) ||
			slices.Contains(groupRecord.GetStringSlice("managers"), user.Id) {
			return true, nil
		}
	}

	return false, nil
}

func userCanShareBlueprintWithUser(e *core.RequestEvent, actingUser *models.User, targetUserRecord *core.Record) (bool, error) {
	if actingUser.IsAdmin() {
		return true, nil
	}

	groupRecords, err := e.App.FindRecordsByFilter(
		"groups",
		"managers.id ?= {:manager_id}",
		"-created",
		0,
		0,
		map[string]any{
			"manager_id": actingUser.Id,
		},
	)
	if err != nil {
		return false, err
	}

	for _, groupRecord := range groupRecords {
		if slices.Contains(groupRecord.GetStringSlice("members"), targetUserRecord.Id) ||
			slices.Contains(groupRecord.GetStringSlice("managers"), targetUserRecord.Id) {
			return true, nil
		}
	}

	return false, nil
}

func getBlueprintAccessType(e *core.RequestEvent, user *models.User, blueprintRecord *core.Record) string {
	if user.IsAdmin() {
		return "admin"
	}

	if blueprintRecord.GetString("owner") == user.Id {
		return "owner"
	}

	if slices.Contains(blueprintRecord.GetStringSlice("sharedUsers"), user.Id) {
		return "direct"
	}

	for _, groupID := range blueprintRecord.GetStringSlice("sharedGroups") {
		groupRecord, err := e.App.FindRecordById("groups", groupID)
		if err != nil {
			continue
		}

		if slices.Contains(groupRecord.GetStringSlice("members"), user.Id) ||
			slices.Contains(groupRecord.GetStringSlice("managers"), user.Id) {
			return "group"
		}
	}

	return "unknown"
}

func resolveUserIDs(e *core.RequestEvent, userRecordIDs []string) []string {
	userIDs := make([]string, 0, len(userRecordIDs))
	for _, userRecordID := range userRecordIDs {
		userID := userRecordID
		userRecord, err := e.App.FindRecordById("users", userRecordID)
		if err == nil {
			resolvedUserID := userRecord.GetString("userID")
			if resolvedUserID != "" {
				userID = resolvedUserID
			}
		}
		userIDs = append(userIDs, userID)
	}
	return userIDs
}

func resolveGroupNames(e *core.RequestEvent, groupRecordIDs []string) []string {
	groupNames := make([]string, 0, len(groupRecordIDs))
	for _, groupRecordID := range groupRecordIDs {
		groupName := groupRecordID
		groupRecord, err := e.App.FindRecordById("groups", groupRecordID)
		if err == nil {
			resolvedGroupName := groupRecord.GetString("name")
			if resolvedGroupName != "" {
				groupName = resolvedGroupName
			}
		}
		groupNames = append(groupNames, groupName)
	}
	return groupNames
}

func resolveOwnerUserID(e *core.RequestEvent, ownerRecordID string) string {
	ownerUserID := ownerRecordID
	ownerRecord, err := e.App.FindRecordById("users", ownerRecordID)
	if err == nil {
		resolvedOwnerUserID := ownerRecord.GetString("userID")
		if resolvedOwnerUserID != "" {
			ownerUserID = resolvedOwnerUserID
		}
	}
	return ownerUserID
}

func normalizeBulkIdentifiers(items []string) []string {
	normalized := make([]string, 0, len(items))
	seen := make(map[string]struct{})

	for _, item := range items {
		parts := strings.Split(item, ",")
		for _, part := range parts {
			value := strings.TrimSpace(part)
			if value == "" {
				continue
			}
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			normalized = append(normalized, value)
		}
	}

	return normalized
}

func buildBlueprintListItem(e *core.RequestEvent, user *models.User, blueprintRecord *core.Record) dto.ListBlueprintsResponseItem {
	return dto.ListBlueprintsResponseItem{
		BlueprintID:  getBlueprintPublicID(blueprintRecord),
		Name:         blueprintRecord.GetString("name"),
		Description:  blueprintRecord.GetString("description"),
		ThumbnailURL: blueprintThumbnailURL(blueprintRecord),
		OwnerUserID:  resolveOwnerUserID(e, blueprintRecord.GetString("owner")),
		SharedUsers:  resolveUserIDs(e, blueprintRecord.GetStringSlice("sharedUsers")),
		SharedGroups: resolveGroupNames(e, blueprintRecord.GetStringSlice("sharedGroups")),
		AccessType:   getBlueprintAccessType(e, user, blueprintRecord),
		Created:      blueprintRecord.GetDateTime("created").Time(),
		Updated:      blueprintRecord.GetDateTime("updated").Time(),
	}
}

type blueprintAccessUserAccumulator struct {
	UserID    string
	Name      string
	AccessSet map[string]struct{}
	GroupSet  map[string]struct{}
}

func sortedKeysFromSet(values map[string]struct{}) []string {
	items := make([]string, 0, len(values))
	for value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		items = append(items, value)
	}
	sort.Strings(items)
	return items
}

func resolveUserIdentity(e *core.RequestEvent, userRecordID string) (string, string) {
	userID := strings.TrimSpace(userRecordID)
	userName := ""

	if userRecordID == "" {
		return userID, userName
	}

	userRecord, err := e.App.FindRecordById("users", userRecordID)
	if err != nil {
		return userID, userName
	}

	resolvedUserID := strings.TrimSpace(userRecord.GetString("userID"))
	if resolvedUserID != "" {
		userID = resolvedUserID
	}

	userName = strings.TrimSpace(userRecord.GetString("name"))
	return userID, userName
}

func readBlueprintConfigBytes(e *core.RequestEvent, blueprintRecord *core.Record) ([]byte, error) {
	configFileName := blueprintRecord.GetString("config")
	if configFileName == "" {
		return nil, fmt.Errorf("blueprint config file is missing")
	}

	filesystemClient, err := e.App.NewFilesystem()
	if err != nil {
		return nil, err
	}
	defer filesystemClient.Close()

	filePath := path.Join(blueprintRecord.BaseFilesPath(), configFileName)
	fileReader, err := filesystemClient.GetFile(filePath)
	if err != nil {
		return nil, err
	}
	defer fileReader.Close()

	fileBytes, err := io.ReadAll(fileReader)
	if err != nil {
		return nil, err
	}

	return fileBytes, nil
}

func validateBlueprintConfigBytes(configBytes []byte) error {
	schemaBytes, err := loadYaml(ludusInstallPath + "/ansible/user-files/range-config.jsonschema")
	if err != nil {
		return fmt.Errorf("can't parse schema: %s", err.Error())
	}

	if err := validateBytes(configBytes, schemaBytes); err != nil {
		return err
	}

	return nil
}

func createBlueprintRecord(e *core.RequestEvent, owner *models.User, blueprintID string, name string, description string, configBytes []byte) (*core.Record, error) {
	blueprintsCollection, err := e.App.FindCollectionByNameOrId("blueprints")
	if err != nil {
		return nil, err
	}

	blueprintRecord := core.NewRecord(blueprintsCollection)
	blueprintRecord.Set("blueprintID", blueprintID)
	blueprintRecord.Set("name", name)
	blueprintRecord.Set("description", description)
	blueprintRecord.Set("owner", owner.Id)

	configFileName := "blueprint-config.yml"
	configFile, err := filesystem.NewFileFromBytes(configBytes, configFileName)
	if err != nil {
		return nil, err
	}
	blueprintRecord.Set("config", configFile)

	if err := e.App.Save(blueprintRecord); err != nil {
		return nil, err
	}

	return blueprintRecord, nil
}

func ListBlueprints(e *core.RequestEvent) error {
	user := e.Get("user").(*models.User)

	blueprintRecords, err := e.App.FindAllRecords("blueprints")
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error listing blueprints: %v", err))
	}

	blueprints := make([]dto.ListBlueprintsResponseItem, 0, len(blueprintRecords))
	for _, blueprintRecord := range blueprintRecords {
		canAccess, err := userCanAccessBlueprint(e, user, blueprintRecord)
		if err != nil {
			return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error checking blueprint access: %v", err))
		}
		if !canAccess {
			continue
		}
		blueprints = append(blueprints, buildBlueprintListItem(e, user, blueprintRecord))
	}

	sort.SliceStable(blueprints, func(i, j int) bool {
		return blueprints[i].Updated.After(blueprints[j].Updated)
	})

	return e.JSON(http.StatusOK, blueprints)
}

func DeleteBlueprint(e *core.RequestEvent) error {
	blueprintRecord, err := getBlueprintRecordFromRequest(e)
	if err != nil {
		return err
	}

	user := e.Get("user").(*models.User)
	if !user.IsAdmin() && blueprintRecord.GetString("owner") != user.Id {
		return JSONError(e, http.StatusForbidden, "You do not own this blueprint and cannot delete it")
	}

	if err := e.App.Delete(blueprintRecord); err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error deleting blueprint: %v", err))
	}

	return JSONResult(e, http.StatusOK, "Blueprint deleted successfully")
}

func getRangeByID(e *core.RequestEvent, rangeID string) (*models.Range, error) {
	rangeRecord, err := e.App.FindFirstRecordByData("ranges", "rangeID", rangeID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("range %s not found", rangeID)
		}
		return nil, err
	}

	rangeObj := &models.Range{}
	rangeObj.SetProxyRecord(rangeRecord)
	return rangeObj, nil
}

func CreateBlueprintFromRange(e *core.RequestEvent) error {
	user := e.Get("user").(*models.User)

	var payload dto.CreateBlueprintFromRangeRequest
	if e.Request.ContentLength > 0 {
		if err := e.BindBody(&payload); err != nil {
			return JSONError(e, http.StatusBadRequest, "Invalid request body: "+err.Error())
		}
	}
	blueprintID := normalizeBlueprintID(payload.BlueprintID)
	if err := validateBlueprintID(blueprintID); err != nil {
		return JSONError(e, http.StatusBadRequest, err.Error())
	}
	blueprintExists, err := blueprintIDExists(e, blueprintID)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error checking if blueprint ID %s is already in use: %v", blueprintID, err))
	}
	if blueprintExists {
		return JSONError(e, http.StatusConflict, fmt.Sprintf("Blueprint ID %s already in use", blueprintID))
	}

	sourceRange := &models.Range{}
	if payload.RangeID != "" {
		rangeNumber, err := GetRangeNumberFromRangeID(payload.RangeID)
		if err != nil {
			return JSONError(e, http.StatusNotFound, fmt.Sprintf("Range %s not found", payload.RangeID))
		}
		if !user.IsAdmin() && !HasRangeAccess(e, user.UserId(), rangeNumber) {
			return JSONError(e, http.StatusForbidden, fmt.Sprintf("You do not have access to range %s", payload.RangeID))
		}
		sourceRange, err = GetRangeObjectByNumber(rangeNumber)
		if err != nil {
			return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error getting range object: %v", err))
		}
	} else {
		var err error
		sourceRange, err = GetRange(e)
		if err != nil {
			return JSONError(e, http.StatusBadRequest, "No source range provided and no range found in request context")
		}
	}

	rangeConfigBytes, err := os.ReadFile(fmt.Sprintf("%s/ranges/%s/range-config.yml", ludusInstallPath, sourceRange.RangeId()))
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error reading range config: %v", err))
	}

	name := payload.Name
	if name == "" {
		name = fmt.Sprintf("%s Blueprint", sourceRange.Name())
	}

	description := payload.Description
	if description == "" {
		description = fmt.Sprintf("Blueprint created from range %s", sourceRange.RangeId())
	}

	blueprintRecord, err := createBlueprintRecord(e, user, blueprintID, name, description, rangeConfigBytes)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error creating blueprint: %v", err))
	}

	return e.JSON(http.StatusCreated, map[string]any{
		"result":      "Blueprint created successfully",
		"blueprintID": getBlueprintPublicID(blueprintRecord),
	})
}

func CopyBlueprint(e *core.RequestEvent) error {
	blueprintRecord, err := getBlueprintRecordFromRequest(e)
	if err != nil {
		return err
	}

	user := e.Get("user").(*models.User)
	canAccess, err := userCanAccessBlueprint(e, user, blueprintRecord)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error checking blueprint access: %v", err))
	}
	if !canAccess {
		return JSONError(e, http.StatusForbidden, "You do not have access to this blueprint")
	}

	var payload dto.CopyBlueprintRequest
	if e.Request.ContentLength > 0 {
		if err := e.BindBody(&payload); err != nil {
			return JSONError(e, http.StatusBadRequest, "Invalid request body: "+err.Error())
		}
	}

	configBytes, err := readBlueprintConfigBytes(e, blueprintRecord)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error reading blueprint config: %v", err))
	}

	name := payload.Name
	if name == "" {
		name = fmt.Sprintf("%s (Copy)", blueprintRecord.GetString("name"))
	}

	description := payload.Description
	if description == "" {
		description = blueprintRecord.GetString("description")
	}
	copyBlueprintID := normalizeBlueprintID(payload.BlueprintID)
	if copyBlueprintID == "" {
		copyBlueprintID, err = getNextCopyBlueprintID(e, getBlueprintPublicID(blueprintRecord))
		if err != nil {
			return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error generating copy blueprint ID: %v", err))
		}
	}
	if err := validateBlueprintID(copyBlueprintID); err != nil {
		return JSONError(e, http.StatusBadRequest, err.Error())
	}
	blueprintExists, err := blueprintIDExists(e, copyBlueprintID)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error checking if blueprint ID %s is already in use: %v", copyBlueprintID, err))
	}
	if blueprintExists {
		return JSONError(e, http.StatusConflict, fmt.Sprintf("Blueprint ID %s already in use", copyBlueprintID))
	}

	copyBlueprintRecord, err := createBlueprintRecord(
		e,
		user,
		copyBlueprintID,
		name,
		description,
		configBytes,
	)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error copying blueprint: %v", err))
	}

	return e.JSON(http.StatusCreated, map[string]any{
		"result":      "Blueprint copied successfully",
		"blueprintID": getBlueprintPublicID(copyBlueprintRecord),
	})
}

func ApplyBlueprintToRange(e *core.RequestEvent) error {
	blueprintRecord, err := getBlueprintRecordFromRequest(e)
	if err != nil {
		return err
	}

	user := e.Get("user").(*models.User)
	canAccess, err := userCanAccessBlueprint(e, user, blueprintRecord)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error checking blueprint access: %v", err))
	}
	if !canAccess {
		return JSONError(e, http.StatusForbidden, "You do not have access to this blueprint")
	}

	var payload dto.ApplyBlueprintRequest
	if err := e.BindBody(&payload); err != nil {
		return JSONError(e, http.StatusBadRequest, "Invalid request body: "+err.Error())
	}

	targetRange := &models.Range{}
	if payload.RangeID != "" {
		targetRange, err = getRangeByID(e, payload.RangeID)
		if err != nil {
			return JSONError(e, http.StatusNotFound, err.Error())
		}
	} else {
		targetRange, err = GetRange(e)
		if err != nil {
			return err
		}
	}

	if !user.IsAdmin() && !HasRangeAccess(e, user.UserId(), targetRange.RangeNumber()) {
		return JSONError(e, http.StatusForbidden, fmt.Sprintf("You do not have access to range %s", targetRange.RangeId()))
	}

	if targetRange.TestingEnabled() && !payload.Force {
		return JSONError(e, http.StatusConflict, "Testing is enabled; to prevent conflicts, the config cannot be updated while testing is enabled. Use --force to override.")
	}

	blueprintConfigBytes, err := readBlueprintConfigBytes(e, blueprintRecord)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error reading blueprint config: %v", err))
	}

	filePath := fmt.Sprintf("%s/ranges/%s/.tmp-range-config.yml", ludusInstallPath, targetRange.RangeId())
	err = os.WriteFile(filePath, blueprintConfigBytes, 0644)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to save temporary range config: "+err.Error())
	}

	originalRange := e.Get("range")
	e.Set("range", targetRange)
	defer func() {
		if originalRange != nil {
			e.Set("range", originalRange)
		}
	}()

	err = validateFile(e, filePath, ludusInstallPath+"/ansible/user-files/range-config.jsonschema")
	if err != nil {
		return JSONError(e, http.StatusBadRequest, "Configuration error: "+err.Error())
	}

	rangeHasRoles := e.Get("rangeHasRoles")
	if rangeHasRoles != nil && rangeHasRoles.(bool) {
		logToFile(fmt.Sprintf("%s/ranges/%s/ansible.log", ludusInstallPath, targetRange.RangeId()), "Resolving dependencies for user-defined roles..\n", false)
		rolesOutput, err := RunLocalAnsiblePlaybookOnTmpRangeConfig(e, []string{fmt.Sprintf("%s/ansible/range-management/user-defined-roles.yml", ludusInstallPath)})
		logToFile(fmt.Sprintf("%s/ranges/%s/ansible.log", ludusInstallPath, targetRange.RangeId()), rolesOutput, true)
		if err != nil {
			targetRange.SetRangeState(LudusRangeStateError)
			saveErr := e.App.Save(targetRange)
			if saveErr != nil {
				logger.Error(fmt.Sprintf("Error saving range: %s", saveErr.Error()))
			}
			errorLine := regexp.MustCompile(`ERROR[^"]*`)
			errorMatch := errorLine.FindString(rolesOutput)
			if errorMatch != "" {
				return JSONError(e, http.StatusBadRequest, "Configuration error: "+errorMatch)
			}
			return JSONError(e, http.StatusBadRequest, fmt.Sprintf("Error generating ordered roles: %s %s", rolesOutput, err))
		}
	}

	err = os.Rename(filePath, fmt.Sprintf("%s/ranges/%s/range-config.yml", ludusInstallPath, targetRange.RangeId()))
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to save the range config")
	}

	return JSONResult(e, http.StatusOK, fmt.Sprintf("Blueprint applied to range %s", targetRange.RangeId()))
}

func GetBlueprintConfig(e *core.RequestEvent) error {
	blueprintRecord, err := getBlueprintRecordFromRequest(e)
	if err != nil {
		return err
	}

	user := e.Get("user").(*models.User)
	canAccess, err := userCanAccessBlueprint(e, user, blueprintRecord)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error checking blueprint access: %v", err))
	}
	if !canAccess {
		return JSONError(e, http.StatusForbidden, "You do not have access to this blueprint")
	}

	blueprintConfigBytes, err := readBlueprintConfigBytes(e, blueprintRecord)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error reading blueprint config: %v", err))
	}

	download := false
	downloadQuery := e.Request.URL.Query().Get("download")
	if downloadQuery != "" {
		download, err = strconv.ParseBool(downloadQuery)
		if err != nil {
			return JSONError(e, http.StatusBadRequest, "Invalid download query parameter")
		}
	}

	if download {
		blueprintName := strings.TrimSpace(blueprintRecord.GetString("name"))
		if blueprintName == "" {
			blueprintName = getBlueprintPublicID(blueprintRecord)
		}
		downloadFileName := strings.ToLower(blueprintName)
		downloadFileName = strings.ReplaceAll(downloadFileName, " ", "-")
		downloadFileName = strings.ReplaceAll(downloadFileName, "/", "-")
		e.Response.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s.yml", downloadFileName))
		e.Response.Header().Set("Content-Type", "application/x-yaml")
		_, writeErr := e.Response.Write(blueprintConfigBytes)
		return writeErr
	}

	return JSONResult(e, http.StatusOK, string(blueprintConfigBytes))
}

func UpdateBlueprintConfig(e *core.RequestEvent) error {
	blueprintRecord, err := getBlueprintRecordFromRequest(e)
	if err != nil {
		return err
	}

	user := e.Get("user").(*models.User)
	if !user.IsAdmin() && blueprintRecord.GetString("owner") != user.Id {
		return JSONError(e, http.StatusForbidden, "You do not own this blueprint and cannot update it")
	}

	var payload dto.UpdateBlueprintConfigRequest
	if err := e.BindBody(&payload); err != nil {
		return JSONError(e, http.StatusBadRequest, "Invalid request body: "+err.Error())
	}

	if strings.TrimSpace(payload.Config) == "" {
		return JSONError(e, http.StatusBadRequest, "Blueprint config cannot be empty")
	}

	configBytes := []byte(payload.Config)
	if err := validateBlueprintConfigBytes(configBytes); err != nil {
		return JSONError(e, http.StatusBadRequest, "Configuration error: "+err.Error())
	}

	configFile, err := filesystem.NewFileFromBytes(configBytes, "blueprint-config.yml")
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error preparing blueprint config file: %v", err))
	}
	blueprintRecord.Set("config", configFile)

	if err := e.App.Save(blueprintRecord); err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error saving blueprint config: %v", err))
	}

	return JSONResult(e, http.StatusOK, "Blueprint config updated successfully")
}

func ShareBlueprintWithGroups(e *core.RequestEvent) error {
	blueprintRecord, err := getBlueprintRecordFromRequest(e)
	if err != nil {
		return err
	}

	actingUser := e.Get("user").(*models.User)
	canAccess, err := userCanAccessBlueprint(e, actingUser, blueprintRecord)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error checking blueprint access: %v", err))
	}
	if !canAccess {
		return JSONError(e, http.StatusForbidden, "You do not have access to this blueprint")
	}

	var bulkRequest dto.BulkShareBlueprintWithGroupsRequest
	if err := e.BindBody(&bulkRequest); err != nil {
		return JSONError(e, http.StatusBadRequest, "Request body with groupNames is required")
	}
	groupNames := normalizeBulkIdentifiers(bulkRequest.GroupNames)
	if len(groupNames) == 0 {
		return JSONError(e, http.StatusBadRequest, "Request body with groupNames is required")
	}

	var success []string
	var errors []dto.BulkBlueprintOperationErrorItem

	for _, groupName := range groupNames {
		groupRecord, err := e.App.FindFirstRecordByData("groups", "name", groupName)
		if err != nil && err != sql.ErrNoRows {
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   groupName,
				Reason: fmt.Sprintf("Error finding group: %v", err),
			})
			continue
		}
		if err == sql.ErrNoRows {
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   groupName,
				Reason: fmt.Sprintf("Group %s not found", groupName),
			})
			continue
		}

		if !actingUser.IsAdmin() && !slices.Contains(groupRecord.GetStringSlice("managers"), actingUser.Id) {
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   groupName,
				Reason: fmt.Sprintf("You are not a manager of group %s", groupName),
			})
			continue
		}

		if slices.Contains(blueprintRecord.GetStringSlice("sharedGroups"), groupRecord.Id) {
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   groupName,
				Reason: fmt.Sprintf("Blueprint already shared with group %s", groupName),
			})
			continue
		}

		blueprintRecord.Set("sharedGroups+", groupRecord.Id)
		if err := e.App.Save(blueprintRecord); err != nil {
			blueprintRecord.Set("sharedGroups-", groupRecord.Id)
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   groupName,
				Reason: fmt.Sprintf("Error sharing blueprint with group: %v", err),
			})
			continue
		}

		success = append(success, groupName)
	}

	return e.JSON(http.StatusOK, dto.BulkBlueprintOperationResponse{
		Success: success,
		Errors:  errors,
	})
}

func UnshareBlueprintWithGroups(e *core.RequestEvent) error {
	blueprintRecord, err := getBlueprintRecordFromRequest(e)
	if err != nil {
		return err
	}

	actingUser := e.Get("user").(*models.User)
	canAccess, err := userCanAccessBlueprint(e, actingUser, blueprintRecord)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error checking blueprint access: %v", err))
	}
	if !canAccess {
		return JSONError(e, http.StatusForbidden, "You do not have access to this blueprint")
	}

	var bulkRequest dto.BulkUnshareBlueprintWithGroupsRequest
	if err := e.BindBody(&bulkRequest); err != nil {
		return JSONError(e, http.StatusBadRequest, "Request body with groupNames is required")
	}
	groupNames := normalizeBulkIdentifiers(bulkRequest.GroupNames)
	if len(groupNames) == 0 {
		return JSONError(e, http.StatusBadRequest, "Request body with groupNames is required")
	}

	var success []string
	var errors []dto.BulkBlueprintOperationErrorItem

	for _, groupName := range groupNames {
		groupRecord, err := e.App.FindFirstRecordByData("groups", "name", groupName)
		if err != nil && err != sql.ErrNoRows {
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   groupName,
				Reason: fmt.Sprintf("Error finding group: %v", err),
			})
			continue
		}
		if err == sql.ErrNoRows {
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   groupName,
				Reason: fmt.Sprintf("Group %s not found", groupName),
			})
			continue
		}

		if !actingUser.IsAdmin() && !slices.Contains(groupRecord.GetStringSlice("managers"), actingUser.Id) {
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   groupName,
				Reason: fmt.Sprintf("You are not a manager of group %s", groupName),
			})
			continue
		}

		if !slices.Contains(blueprintRecord.GetStringSlice("sharedGroups"), groupRecord.Id) {
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   groupName,
				Reason: fmt.Sprintf("Blueprint is not shared with group %s", groupName),
			})
			continue
		}

		blueprintRecord.Set("sharedGroups-", groupRecord.Id)
		if err := e.App.Save(blueprintRecord); err != nil {
			blueprintRecord.Set("sharedGroups+", groupRecord.Id)
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   groupName,
				Reason: fmt.Sprintf("Error removing group share: %v", err),
			})
			continue
		}

		success = append(success, groupName)
	}

	return e.JSON(http.StatusOK, dto.BulkBlueprintOperationResponse{
		Success: success,
		Errors:  errors,
	})
}

func ShareBlueprintWithUsers(e *core.RequestEvent) error {
	blueprintRecord, err := getBlueprintRecordFromRequest(e)
	if err != nil {
		return err
	}

	actingUser := e.Get("user").(*models.User)
	canAccess, err := userCanAccessBlueprint(e, actingUser, blueprintRecord)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error checking blueprint access: %v", err))
	}
	if !canAccess {
		return JSONError(e, http.StatusForbidden, "You do not have access to this blueprint")
	}

	var bulkRequest dto.BulkShareBlueprintWithUsersRequest
	if err := e.BindBody(&bulkRequest); err != nil {
		return JSONError(e, http.StatusBadRequest, "Request body with userIDs is required")
	}
	userIDs := normalizeBulkIdentifiers(bulkRequest.UserIDs)
	if len(userIDs) == 0 {
		return JSONError(e, http.StatusBadRequest, "Request body with userIDs is required")
	}

	var success []string
	var errors []dto.BulkBlueprintOperationErrorItem

	for _, userID := range userIDs {
		targetUserRecord, err := e.App.FindFirstRecordByData("users", "userID", userID)
		if err != nil && err != sql.ErrNoRows {
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   userID,
				Reason: fmt.Sprintf("Error finding user: %v", err),
			})
			continue
		}
		if err == sql.ErrNoRows {
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   userID,
				Reason: fmt.Sprintf("User %s not found", userID),
			})
			continue
		}

		canShareWithUser, err := userCanShareBlueprintWithUser(e, actingUser, targetUserRecord)
		if err != nil {
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   userID,
				Reason: fmt.Sprintf("Error checking manager permissions: %v", err),
			})
			continue
		}
		if !canShareWithUser {
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   userID,
				Reason: fmt.Sprintf("You are not a manager of a group that contains user %s", userID),
			})
			continue
		}

		if slices.Contains(blueprintRecord.GetStringSlice("sharedUsers"), targetUserRecord.Id) {
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   userID,
				Reason: fmt.Sprintf("Blueprint already shared with user %s", userID),
			})
			continue
		}

		blueprintRecord.Set("sharedUsers+", targetUserRecord.Id)
		if err := e.App.Save(blueprintRecord); err != nil {
			blueprintRecord.Set("sharedUsers-", targetUserRecord.Id)
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   userID,
				Reason: fmt.Sprintf("Error sharing blueprint with user: %v", err),
			})
			continue
		}

		success = append(success, userID)
	}

	return e.JSON(http.StatusOK, dto.BulkBlueprintOperationResponse{
		Success: success,
		Errors:  errors,
	})
}

func UnshareBlueprintWithUsers(e *core.RequestEvent) error {
	blueprintRecord, err := getBlueprintRecordFromRequest(e)
	if err != nil {
		return err
	}

	actingUser := e.Get("user").(*models.User)
	canAccess, err := userCanAccessBlueprint(e, actingUser, blueprintRecord)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error checking blueprint access: %v", err))
	}
	if !canAccess {
		return JSONError(e, http.StatusForbidden, "You do not have access to this blueprint")
	}

	var bulkRequest dto.BulkUnshareBlueprintWithUsersRequest
	if err := e.BindBody(&bulkRequest); err != nil {
		return JSONError(e, http.StatusBadRequest, "Request body with userIDs is required")
	}
	userIDs := normalizeBulkIdentifiers(bulkRequest.UserIDs)
	if len(userIDs) == 0 {
		return JSONError(e, http.StatusBadRequest, "Request body with userIDs is required")
	}

	var success []string
	var errors []dto.BulkBlueprintOperationErrorItem

	for _, userID := range userIDs {
		targetUserRecord, err := e.App.FindFirstRecordByData("users", "userID", userID)
		if err != nil && err != sql.ErrNoRows {
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   userID,
				Reason: fmt.Sprintf("Error finding user: %v", err),
			})
			continue
		}
		if err == sql.ErrNoRows {
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   userID,
				Reason: fmt.Sprintf("User %s not found", userID),
			})
			continue
		}

		canShareWithUser, err := userCanShareBlueprintWithUser(e, actingUser, targetUserRecord)
		if err != nil {
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   userID,
				Reason: fmt.Sprintf("Error checking manager permissions: %v", err),
			})
			continue
		}
		if !canShareWithUser {
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   userID,
				Reason: fmt.Sprintf("You are not a manager of a group that contains user %s", userID),
			})
			continue
		}

		if !slices.Contains(blueprintRecord.GetStringSlice("sharedUsers"), targetUserRecord.Id) {
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   userID,
				Reason: fmt.Sprintf("Blueprint is not shared with user %s", userID),
			})
			continue
		}

		blueprintRecord.Set("sharedUsers-", targetUserRecord.Id)
		if err := e.App.Save(blueprintRecord); err != nil {
			blueprintRecord.Set("sharedUsers+", targetUserRecord.Id)
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   userID,
				Reason: fmt.Sprintf("Error removing user share: %v", err),
			})
			continue
		}

		success = append(success, userID)
	}

	return e.JSON(http.StatusOK, dto.BulkBlueprintOperationResponse{
		Success: success,
		Errors:  errors,
	})
}

func ListBlueprintAccessUsers(e *core.RequestEvent) error {
	blueprintRecord, err := getBlueprintRecordFromRequest(e)
	if err != nil {
		return err
	}

	actingUser := e.Get("user").(*models.User)
	canAccess, err := userCanAccessBlueprint(e, actingUser, blueprintRecord)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error checking blueprint access: %v", err))
	}
	if !canAccess {
		return JSONError(e, http.StatusForbidden, "You do not have access to this blueprint")
	}

	userIdentityCache := make(map[string]struct {
		UserID string
		Name   string
	})
	accessByUserRecordID := make(map[string]*blueprintAccessUserAccumulator)

	addUserAccess := func(userRecordID string, accessType string, groupName string) {
		recordID := strings.TrimSpace(userRecordID)
		if recordID == "" {
			return
		}

		identity, exists := userIdentityCache[recordID]
		if !exists {
			resolvedUserID, resolvedName := resolveUserIdentity(e, recordID)
			identity = struct {
				UserID string
				Name   string
			}{
				UserID: resolvedUserID,
				Name:   resolvedName,
			}
			userIdentityCache[recordID] = identity
		}

		if strings.TrimSpace(identity.UserID) == "" {
			return
		}

		accessEntry, exists := accessByUserRecordID[recordID]
		if !exists {
			accessEntry = &blueprintAccessUserAccumulator{
				UserID:    identity.UserID,
				Name:      identity.Name,
				AccessSet: make(map[string]struct{}),
				GroupSet:  make(map[string]struct{}),
			}
			accessByUserRecordID[recordID] = accessEntry
		}

		normalizedAccessType := strings.TrimSpace(accessType)
		if normalizedAccessType != "" {
			accessEntry.AccessSet[normalizedAccessType] = struct{}{}
		}

		normalizedGroupName := strings.TrimSpace(groupName)
		if normalizedGroupName != "" {
			accessEntry.GroupSet[normalizedGroupName] = struct{}{}
		}
	}

	addUserAccess(blueprintRecord.GetString("owner"), "owner", "")

	for _, sharedUserRecordID := range blueprintRecord.GetStringSlice("sharedUsers") {
		addUserAccess(sharedUserRecordID, "direct", "")
	}

	for _, sharedGroupRecordID := range blueprintRecord.GetStringSlice("sharedGroups") {
		groupRecord, err := e.App.FindRecordById("groups", sharedGroupRecordID)
		if err != nil {
			continue
		}

		groupName := strings.TrimSpace(groupRecord.GetString("name"))
		if groupName == "" {
			groupName = sharedGroupRecordID
		}

		for _, managerRecordID := range groupRecord.GetStringSlice("managers") {
			addUserAccess(managerRecordID, "group-manager", groupName)
		}
		for _, memberRecordID := range groupRecord.GetStringSlice("members") {
			addUserAccess(memberRecordID, "group-member", groupName)
		}
	}

	response := make([]dto.ListBlueprintAccessUsersResponseItem, 0, len(accessByUserRecordID))
	for _, accessEntry := range accessByUserRecordID {
		response = append(response, dto.ListBlueprintAccessUsersResponseItem{
			UserID: accessEntry.UserID,
			Name:   accessEntry.Name,
			Access: sortedKeysFromSet(accessEntry.AccessSet),
			Groups: sortedKeysFromSet(accessEntry.GroupSet),
		})
	}

	sort.SliceStable(response, func(i, j int) bool {
		return response[i].UserID < response[j].UserID
	})

	return e.JSON(http.StatusOK, response)
}

func ListBlueprintAccessGroups(e *core.RequestEvent) error {
	blueprintRecord, err := getBlueprintRecordFromRequest(e)
	if err != nil {
		return err
	}

	actingUser := e.Get("user").(*models.User)
	canAccess, err := userCanAccessBlueprint(e, actingUser, blueprintRecord)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error checking blueprint access: %v", err))
	}
	if !canAccess {
		return JSONError(e, http.StatusForbidden, "You do not have access to this blueprint")
	}

	response := make([]dto.ListBlueprintAccessGroupsResponseItem, 0, len(blueprintRecord.GetStringSlice("sharedGroups")))
	for _, sharedGroupRecordID := range blueprintRecord.GetStringSlice("sharedGroups") {
		groupRecord, err := e.App.FindRecordById("groups", sharedGroupRecordID)
		if err != nil {
			continue
		}

		groupName := strings.TrimSpace(groupRecord.GetString("name"))
		if groupName == "" {
			groupName = sharedGroupRecordID
		}

		managers := resolveUserIDs(e, groupRecord.GetStringSlice("managers"))
		members := resolveUserIDs(e, groupRecord.GetStringSlice("members"))
		sort.Strings(managers)
		sort.Strings(members)

		response = append(response, dto.ListBlueprintAccessGroupsResponseItem{
			GroupName: groupName,
			Managers:  managers,
			Members:   members,
		})
	}

	sort.SliceStable(response, func(i, j int) bool {
		return response[i].GroupName < response[j].GroupName
	})

	return e.JSON(http.StatusOK, response)
}
