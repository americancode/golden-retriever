package npm

import "strings"

func selectedScanKeys(state *State, source string) []string {
	keys := make([]string, 0)
	switch strings.ToLower(source) {
	case "target":
		for k := range state.Target {
			keys = append(keys, k)
		}
	case "both":
		seen := map[string]struct{}{}
		for k := range state.Local {
			seen[k] = struct{}{}
			keys = append(keys, k)
		}
		for k := range state.Target {
			if _, ok := seen[k]; !ok {
				keys = append(keys, k)
			}
		}
	default:
		for k := range state.Local {
			keys = append(keys, k)
		}
	}
	return keys
}

func getStateRecord(state *State, key, source string) (StateRecord, string) {
	if strings.EqualFold(source, "target") {
		return state.Target[key], "target"
	}
	if rec, ok := state.Local[key]; ok {
		return rec, "local"
	}
	if strings.EqualFold(source, "both") {
		if rec, ok := state.Target[key]; ok {
			return rec, "target"
		}
	}
	return state.Local[key], "local"
}

func setStateRecord(state *State, key, bucket string, rec StateRecord) {
	if bucket == "target" {
		state.Target[key] = rec
		return
	}
	state.Local[key] = rec
}
