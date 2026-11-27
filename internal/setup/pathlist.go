package setup

import "strings"

// samePathEntry compares two PATH entries the way Windows resolves them:
// ignoring case, surrounding spaces and a trailing separator.
func samePathEntry(a, b string) bool {
	norm := func(s string) string {
		return strings.TrimRight(strings.TrimSpace(s), `\/`)
	}
	return strings.EqualFold(norm(a), norm(b))
}

// addPathEntry appends dir to the PATH value list unless it is already
// there. It reports whether the list changed.
func addPathEntry(list, dir string) (string, bool) {
	for _, e := range strings.Split(list, ";") {
		if samePathEntry(e, dir) {
			return list, false
		}
	}
	if list == "" {
		return dir, true
	}
	if !strings.HasSuffix(list, ";") {
		list += ";"
	}
	return list + dir, true
}

// removePathEntry removes every occurrence of dir from the PATH value list,
// keeping the other entries as they are. It reports whether the list
// changed.
func removePathEntry(list, dir string) (string, bool) {
	parts := strings.Split(list, ";")
	kept := parts[:0]
	for _, e := range parts {
		if e != "" && samePathEntry(e, dir) {
			continue
		}
		kept = append(kept, e)
	}
	if len(kept) == len(parts) {
		return list, false
	}
	return strings.Join(kept, ";"), true
}
