package npm

import "strings"

func appendUniqueStrings(base []string, values ...string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(base)+len(values))
	for _, value := range append(base, values...) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func appendVulnDescription(base []string, id, description string) []string {
	description = strings.TrimSpace(description)
	if description == "" {
		return base
	}
	id = strings.TrimSpace(id)
	if id != "" && !strings.HasPrefix(description, id+": ") {
		description = id + ": " + description
	}
	return appendUniqueStrings(base, description)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
