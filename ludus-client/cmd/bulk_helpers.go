package cmd

import (
	"fmt"
	"ludus/logger"
	"strings"
)

type bulkOperationError struct {
	Item   string
	Reason string
}

func joinOrDash(items []string) string {
	filtered := removeEmptyStrings(items)
	if len(filtered) == 0 {
		return "-"
	}
	return strings.Join(filtered, ", ")
}

func parseIdentifierInputs(args []string, dedupe bool, dropEmpty bool) []string {
	parsed := make([]string, 0, len(args))
	var seen map[string]struct{}
	if dedupe {
		seen = make(map[string]struct{})
	}

	for _, rawArg := range args {
		parts := strings.Split(rawArg, ",")
		for _, part := range parts {
			item := strings.TrimSpace(part)
			if item == "" && dropEmpty {
				continue
			}
			if dedupe {
				if _, exists := seen[item]; exists {
					continue
				}
				seen[item] = struct{}{}
			}
			parsed = append(parsed, item)
		}
	}

	return parsed
}

func parseBulkIdentifiers(args []string) []string {
	return parseIdentifierInputs(args, true, true)
}

func splitAndTrimIDs(idsArg string) []string {
	return parseIdentifierInputs([]string{idsArg}, false, false)
}

func printBulkOperationResult(action string, itemType string, success []string, errors []bulkOperationError) {
	if len(success) > 0 {
		logger.Logger.Info(fmt.Sprintf("Successfully %s %d %s: %v", action, len(success), itemType, success))
	}

	if len(errors) == 0 {
		return
	}

	logger.Logger.Warn(fmt.Sprintf("Failed to process %d %s:", len(errors), itemType))
	for _, err := range errors {
		logger.Logger.Warn(fmt.Sprintf("  %s: %s", err.Item, err.Reason))
	}
}
