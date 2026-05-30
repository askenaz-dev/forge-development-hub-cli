package consumermanifest

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Constraint is a parsed SemVer constraint expression.
//
// The package supports the following constraint forms (npm-style,
// 0.x semantics respected):
//
//   - "" / "*" / "latest"  → any version
//   - "X.Y.Z" / "vX.Y.Z"   → exact match
//   - "^X.Y(.Z)?"          → caret: same-non-zero-major (for 0.x, same minor)
//   - "~X.Y(.Z)?"          → tilde: same-major-and-minor
//
// Pre-release identifiers and build metadata are accepted in exact
// matches but ignored by caret/tilde range computation (a follow-up
// can refine this).
type Constraint struct {
	raw string

	any   bool // matches any
	op    string
	minor *int
	patch *int
	major int

	exact string
}

var semverPartRE = regexp.MustCompile(`^v?(\d+)(?:\.(\d+))?(?:\.(\d+))?(?:[-+].*)?$`)

// ParseConstraint parses s into a Constraint. Returns an error when
// s is malformed.
func ParseConstraint(s string) (*Constraint, error) {
	t := strings.TrimSpace(s)
	if t == "" || t == "*" || t == "latest" {
		return &Constraint{raw: s, any: true}, nil
	}
	c := &Constraint{raw: s}
	op := ""
	if strings.HasPrefix(t, "^") || strings.HasPrefix(t, "~") {
		op = t[:1]
		t = t[1:]
	}
	m := semverPartRE.FindStringSubmatch(t)
	if m == nil {
		return nil, fmt.Errorf("invalid version constraint %q", s)
	}
	majorN, _ := strconv.Atoi(m[1])
	c.major = majorN
	c.op = op
	if m[2] != "" {
		minorN, _ := strconv.Atoi(m[2])
		c.minor = &minorN
	}
	if m[3] != "" {
		patchN, _ := strconv.Atoi(m[3])
		c.patch = &patchN
	}
	if op == "" {
		// Exact requires X.Y.Z (or X.Y, treated as X.Y.0 for matching).
		c.exact = canonicalExact(m[1], m[2], m[3])
	}
	return c, nil
}

func canonicalExact(maj, min, pat string) string {
	if min == "" {
		return maj + ".0.0"
	}
	if pat == "" {
		return maj + "." + min + ".0"
	}
	return maj + "." + min + "." + pat
}

// Matches reports whether v satisfies the constraint c.
func (c *Constraint) Matches(v string) bool {
	if c == nil || c.any {
		return true
	}
	vv, err := parseSemver(v)
	if err != nil {
		return false
	}
	switch c.op {
	case "":
		ev, err := parseSemver(c.exact)
		if err != nil {
			return false
		}
		return ev.equal(vv)
	case "^":
		// 0.x caret: same minor; otherwise: same major.
		if c.major == 0 {
			if c.minor == nil {
				// "^0" — match anything in 0.x.x.
				return vv.major == 0
			}
			return vv.major == 0 && vv.minor == *c.minor && vv.gte(c.minor, c.patch)
		}
		return vv.major == c.major && vv.gte(c.minor, c.patch)
	case "~":
		// Tilde: same major + minor.
		if c.minor == nil {
			return vv.major == c.major
		}
		minorPin := *c.minor
		return vv.major == c.major && vv.minor == minorPin && vv.gte(c.minor, c.patch)
	}
	return false
}

// String returns the original constraint string (for error messages).
func (c *Constraint) String() string { return c.raw }

type semver struct {
	major, minor, patch int
	pre                 string
}

func parseSemver(s string) (semver, error) {
	t := strings.TrimSpace(s)
	if t == "" {
		return semver{}, fmt.Errorf("empty version")
	}
	t = strings.TrimPrefix(t, "v")
	pre := ""
	if i := strings.IndexAny(t, "-+"); i >= 0 {
		pre = t[i:]
		t = t[:i]
	}
	parts := strings.Split(t, ".")
	if len(parts) < 1 || len(parts) > 3 {
		return semver{}, fmt.Errorf("invalid semver %q", s)
	}
	maj, err := strconv.Atoi(parts[0])
	if err != nil {
		return semver{}, fmt.Errorf("invalid semver %q", s)
	}
	out := semver{major: maj, pre: pre}
	if len(parts) > 1 {
		out.minor, err = strconv.Atoi(parts[1])
		if err != nil {
			return semver{}, fmt.Errorf("invalid semver %q", s)
		}
	}
	if len(parts) > 2 {
		out.patch, err = strconv.Atoi(parts[2])
		if err != nil {
			return semver{}, fmt.Errorf("invalid semver %q", s)
		}
	}
	return out, nil
}

func (a semver) equal(b semver) bool {
	return a.major == b.major && a.minor == b.minor && a.patch == b.patch && a.pre == b.pre
}

// gte reports whether the semver is greater-or-equal to (minor.patch).
func (a semver) gte(minor, patch *int) bool {
	if minor != nil {
		if a.minor < *minor {
			return false
		}
		if a.minor > *minor {
			return true
		}
		if patch != nil && a.patch < *patch {
			return false
		}
	}
	return true
}

// HighestSatisfying picks the highest version in versions that
// matches c. Returns "" when no version matches.
func (c *Constraint) HighestSatisfying(versions []string) string {
	best := ""
	var bestSV semver
	for _, v := range versions {
		if !c.Matches(v) {
			continue
		}
		sv, err := parseSemver(v)
		if err != nil {
			continue
		}
		if best == "" || sv.greater(bestSV) {
			best = v
			bestSV = sv
		}
	}
	return best
}

func (a semver) greater(b semver) bool {
	if a.major != b.major {
		return a.major > b.major
	}
	if a.minor != b.minor {
		return a.minor > b.minor
	}
	return a.patch > b.patch
}
