package ludusapi

import (
	"fmt"
	"regexp"
	"strings"
)

// sanitizeNameToRangeID converts a human-readable range name into a short, valid Proxmox pool name.
// Multi-word names: first letter of each word, capitalized (e.g., "My Test Range" → "MTR").
// Single-word names: first 3 letters, capitalized (e.g., "demo" → "DEM").
// Returns empty string if the name contains no usable letters.
func sanitizeNameToRangeID(name string) string {
	// Strip characters not matching letters, digits, spaces, hyphens, underscores
	validChars := regexp.MustCompile(`[^A-Za-z0-9 _\-]`)
	cleaned := validChars.ReplaceAllString(name, "")
	cleaned = strings.TrimSpace(cleaned)

	if cleaned == "" {
		return ""
	}

	// Split on whitespace, hyphens, and underscores to get words
	splitter := regexp.MustCompile(`[\s_\-]+`)
	words := splitter.Split(cleaned, -1)

	// Filter out empty strings and digit-only words
	letterWord := regexp.MustCompile(`[A-Za-z]`)
	var validWords []string
	for _, w := range words {
		if letterWord.MatchString(w) {
			validWords = append(validWords, w)
		}
	}

	if len(validWords) == 0 {
		return ""
	}

	var result string
	if len(validWords) > 1 {
		// Multiple words: take first letter of each word, capitalize
		for _, w := range validWords {
			// Find the first letter in the word
			for _, ch := range w {
				if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') {
					result += strings.ToUpper(string(ch))
					break
				}
			}
		}
	} else {
		// Single word: take first 3 letters, capitalize
		word := validWords[0]
		count := 0
		for _, ch := range word {
			if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') {
				result += strings.ToUpper(string(ch))
				count++
				if count >= 3 {
					break
				}
			}
		}
	}

	return result
}

// resolveUniqueRangeID takes a base rangeID and appends _2, _3, etc. until
// the existsFn callback returns false. existsFn should check both DB and Proxmox.
func resolveUniqueRangeID(baseID string, existsFn func(string) bool) (string, error) {
	if baseID == "" {
		return "", fmt.Errorf("range name must contain at least one alphanumeric character, or provide an explicit Range ID")
	}

	candidate := baseID
	if !existsFn(candidate) {
		return candidate, nil
	}

	for i := 2; i <= 1000; i++ {
		candidate = fmt.Sprintf("%s_%d", baseID, i)
		// Respect 64-char limit
		if len(candidate) > 64 {
			candidate = candidate[:64]
		}
		if !existsFn(candidate) {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("could not find a unique range ID after 1000 attempts based on %q", baseID)
}
