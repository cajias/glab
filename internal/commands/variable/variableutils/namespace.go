package variableutils

import "strings"

// GroupChain returns a namespace path and the group paths above it, closest
// first. CI resolves a variable in that order, so a lookup that walks the
// group tree must use the same order.
//
//	group/subgroup -> [group/subgroup, group]
//	group          -> [group]
//	""             -> nil
func GroupChain(namespace string) []string {
	namespace = strings.Trim(namespace, "/")
	if namespace == "" {
		return nil
	}

	segments := strings.Split(namespace, "/")
	chain := make([]string, 0, len(segments))
	for i := len(segments); i > 0; i-- {
		chain = append(chain, strings.Join(segments[:i], "/"))
	}
	return chain
}

// ParentNamespace returns the namespace that holds a project or group path.
// A top-level path has nothing above it, so the result is empty.
//
//	acme/platform/api -> acme/platform
//	acme/api          -> acme
func ParentNamespace(fullPath string) string {
	fullPath = strings.Trim(fullPath, "/")

	index := strings.LastIndex(fullPath, "/")
	if index < 0 {
		return ""
	}
	return fullPath[:index]
}
