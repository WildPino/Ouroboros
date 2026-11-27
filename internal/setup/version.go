package setup

import (
	"regexp"
	"strconv"
)

// versionRE matches release tags and the "git describe" form of builds
// after them: v0.2.0, v0.2.0-rc.1, v0.2.0-3-gabc1234, v0.2.0-3-gabc1234-dirty.
var versionRE = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.]+?))??(?:-(\d+)-g[0-9a-f]+)?(?:-dirty)?$`)

type parsedVersion struct {
	nums    [3]int
	pre     string
	commits int
}

func parseVersion(s string) (parsedVersion, bool) {
	m := versionRE.FindStringSubmatch(s)
	if m == nil {
		return parsedVersion{}, false
	}
	var v parsedVersion
	for i := range v.nums {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return parsedVersion{}, false
		}
		v.nums[i] = n
	}
	v.pre = m[4]
	if m[5] != "" {
		n, err := strconv.Atoi(m[5])
		if err != nil {
			return parsedVersion{}, false
		}
		v.commits = n
	}
	return v, true
}

// CompareVersions returns -1, 0 or 1 as a is older than, the same as or
// newer than b. ok is false when either is not a release version, as in a
// development build, and then no order is known.
//
// A pre-release is older than its release; a build made some commits after
// a tag is newer than the tag. Pre-release labels compare as text.
func CompareVersions(a, b string) (cmp int, ok bool) {
	va, okA := parseVersion(a)
	vb, okB := parseVersion(b)
	if !okA || !okB {
		return 0, false
	}
	for i := range va.nums {
		if c := compareInt(va.nums[i], vb.nums[i]); c != 0 {
			return c, true
		}
	}
	switch {
	case va.pre == vb.pre:
	case va.pre == "":
		return 1, true
	case vb.pre == "":
		return -1, true
	case va.pre < vb.pre:
		return -1, true
	default:
		return 1, true
	}
	return compareInt(va.commits, vb.commits), true
}

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
