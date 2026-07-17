package artifact

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

func validateRelativePath(value string) (string, error) {
	if value == "" || strings.ContainsRune(value, 0) || filepath.IsAbs(value) {
		return "", fmt.Errorf("artifact path must be a non-empty relative path")
	}
	clean := filepath.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("artifact path %q escapes the outbox", value)
	}
	return clean, nil
}

func validateMetadata(value, name string) (string, error) {
	value = strings.TrimSpace(value)
	if len([]rune(value)) > 256 {
		return "", fmt.Errorf("artifact %s is too long", name)
	}
	for _, r := range value {
		if r == 0 || unicode.IsControl(r) {
			return "", fmt.Errorf("artifact %s contains a control character", name)
		}
	}
	return value, nil
}
