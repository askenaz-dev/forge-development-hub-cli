// Package instincts implements the bottom-up knowledge loop introduced by the
// `add-instinct-collaboration` change. Devs capture domain patterns ("instincts")
// during sessions; instincts are stored locally as YAML files under
// `~/.fdh/instincts/<id>.yaml`; they can be exported/imported via bundles;
// and admins can run `fdh evolve` to cluster instincts and generate skill drafts.
//
// File-based, no backend. Compose with the existing `~/.fdh/state.json` for
// per-machine counters and with `fdh-scan` for pre-export safety screening.
package instincts

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// SchemaVersion of the instinct file format.
const SchemaVersion = 1

// Instinct is the in-memory representation of one ~/.fdh/instincts/<id>.yaml.
//
// Layout on disk: a YAML document with a `---`-delimited frontmatter block at
// the top followed by a Markdown body. The frontmatter is mapped onto the
// fields below; the body is plain text (markdown).
type Instinct struct {
	// ID is a ULID (26 chars, Crockford Base32). Lexicographically sortable.
	ID string `yaml:"id"`

	// Title is a one-liner (≤120 chars) summarising the pattern.
	Title string `yaml:"title"`

	// Confidence in 0.0–1.0. Manual input in v1. Suggested anchors:
	//   0.3 = initial idea
	//   0.6 = observed pattern (3+ times)
	//   0.9 = universal rule within the domain
	Confidence float64 `yaml:"confidence"`

	// Domain is a free-form kebab-case taxonomy (e.g. "backend-services-go",
	// "frontend-checkout", "data-pipelines-airflow"). Normalised by convention,
	// not enforced by validation.
	Domain string `yaml:"domain"`

	// CapturedBy is an identifier of the dev who captured the instinct.
	// Usually an email pulled from ~/.fdh/config.yaml (FDH_USER_EMAIL overrides).
	CapturedBy string `yaml:"captured_by"`

	// CapturedAt is the ISO 8601 timestamp (with timezone) when the instinct
	// was first captured. Edits do NOT update this field.
	CapturedAt time.Time `yaml:"captured_at"`

	// Context is auto-populated metadata about where the instinct was captured.
	Context Context `yaml:"context"`

	// Tags is a list of free-form strings used for clustering + filtering.
	Tags []string `yaml:"tags,omitempty"`

	// RelatedSkills is an optional list of skill names from the hub that this
	// instinct relates to (used for cross-reference; not enforced).
	RelatedSkills []string `yaml:"related_skills,omitempty"`

	// Body is the free-form markdown body (everything after the closing `---`).
	// Not serialised as part of frontmatter — see Encode/Decode below.
	Body string `yaml:"-"`
}

// Context is the auto-populated provenance block.
type Context struct {
	// ProjectHint is the basename of the cwd at capture time (path-agnostic).
	ProjectHint string `yaml:"project_hint,omitempty"`

	// HubCommit is the SHA of the hub at capture time, if resolvable from the
	// active project's .fdh/lock.yaml.
	HubCommit string `yaml:"hub_commit,omitempty"`

	// Triggers is a free-form list of strings describing the conditions under
	// which this pattern was discovered. Synthetic metadata, not the dev's body.
	Triggers []string `yaml:"triggers,omitempty"`
}

// -----------------------------------------------------------------------------
// Validation
// -----------------------------------------------------------------------------

// ulidPattern enforces the 26-char Crockford Base32 alphabet of ULIDs.
var ulidPattern = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)

// ErrInvalid is returned by Validate when an instinct fails its frontmatter checks.
type ErrInvalid struct{ Reason string }

func (e *ErrInvalid) Error() string { return "invalid instinct: " + e.Reason }

// Validate checks the instinct's required fields and value ranges.
func (i *Instinct) Validate() error {
	if !ulidPattern.MatchString(i.ID) {
		return &ErrInvalid{Reason: fmt.Sprintf("id %q is not a valid ULID (26 Crockford Base32 chars)", i.ID)}
	}
	if strings.TrimSpace(i.Title) == "" {
		return &ErrInvalid{Reason: "title must not be empty"}
	}
	if len(i.Title) > 120 {
		return &ErrInvalid{Reason: fmt.Sprintf("title is %d chars; max 120", len(i.Title))}
	}
	if i.Confidence < 0.0 || i.Confidence > 1.0 {
		return &ErrInvalid{Reason: fmt.Sprintf("confidence %.3f out of range [0.0, 1.0]", i.Confidence)}
	}
	if strings.TrimSpace(i.Domain) == "" {
		return &ErrInvalid{Reason: "domain must not be empty"}
	}
	if strings.TrimSpace(i.CapturedBy) == "" {
		return &ErrInvalid{Reason: "captured_by must not be empty (set FDH_USER_EMAIL or run `fdh config set user.email`)"}
	}
	if i.CapturedAt.IsZero() {
		return &ErrInvalid{Reason: "captured_at must not be the zero time"}
	}
	if strings.TrimSpace(i.Body) == "" {
		return &ErrInvalid{Reason: "body must not be empty"}
	}
	return nil
}

// -----------------------------------------------------------------------------
// Encode / Decode
// -----------------------------------------------------------------------------

const fmDelim = "---\n"

// Encode renders the instinct as the on-disk YAML+markdown format.
//
//	---
//	<yaml frontmatter>
//	---
//
//	<markdown body>
func (i *Instinct) Encode() ([]byte, error) {
	if err := i.Validate(); err != nil {
		return nil, err
	}
	// Marshal a shallow copy so we don't accidentally serialise the Body field.
	fm, err := yaml.Marshal(struct {
		ID            string    `yaml:"id"`
		Title         string    `yaml:"title"`
		Confidence    float64   `yaml:"confidence"`
		Domain        string    `yaml:"domain"`
		CapturedBy    string    `yaml:"captured_by"`
		CapturedAt    time.Time `yaml:"captured_at"`
		Context       Context   `yaml:"context"`
		Tags          []string  `yaml:"tags,omitempty"`
		RelatedSkills []string  `yaml:"related_skills,omitempty"`
	}{
		ID:            i.ID,
		Title:         i.Title,
		Confidence:    i.Confidence,
		Domain:        i.Domain,
		CapturedBy:    i.CapturedBy,
		CapturedAt:    i.CapturedAt.UTC(),
		Context:       i.Context,
		Tags:          i.Tags,
		RelatedSkills: i.RelatedSkills,
	})
	if err != nil {
		return nil, fmt.Errorf("encode frontmatter: %w", err)
	}
	body := strings.TrimRight(i.Body, "\n") + "\n"
	return []byte(fmDelim + string(fm) + fmDelim + "\n" + body), nil
}

// Decode parses the on-disk format back into an Instinct.
func Decode(data []byte) (*Instinct, error) {
	text := string(data)
	if !strings.HasPrefix(text, fmDelim) {
		return nil, &ErrInvalid{Reason: "missing opening `---` frontmatter delimiter"}
	}
	rest := text[len(fmDelim):]
	end := strings.Index(rest, "\n"+strings.TrimRight(fmDelim, "\n")+"\n")
	if end < 0 {
		// Be permissive about trailing whitespace before the closing delim.
		end = strings.Index(rest, "\n---")
		if end < 0 {
			return nil, &ErrInvalid{Reason: "missing closing `---` frontmatter delimiter"}
		}
	}
	fmText := rest[:end]
	bodyStart := end + len("\n---")
	if bodyStart > len(rest) {
		bodyStart = len(rest)
	}
	body := strings.TrimLeft(rest[bodyStart:], "\n")

	i := &Instinct{}
	if err := yaml.Unmarshal([]byte(fmText), i); err != nil {
		return nil, &ErrInvalid{Reason: fmt.Sprintf("frontmatter YAML parse: %v", err)}
	}
	i.Body = body
	return i, nil
}

// -----------------------------------------------------------------------------
// ULID generation
// -----------------------------------------------------------------------------

// Crockford Base32 alphabet, no I/L/O/U.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewULID returns a new ULID for the given moment (use time.Now() for live capture).
// Implementation: 48-bit unix-ms timestamp + 80-bit cryptographic randomness,
// encoded as 26 chars of Crockford Base32.
//
// Lexicographic ordering of ULIDs equals chronological ordering when generated
// on the same machine (clock skew between machines is the usual caveat).
func NewULID(t time.Time) (string, error) {
	ms := uint64(t.UnixMilli())
	if t.Before(time.Unix(0, 0)) {
		return "", errors.New("ulid: cannot encode timestamp before 1970-01-01")
	}
	var raw [16]byte // 128 bits total
	raw[0] = byte(ms >> 40)
	raw[1] = byte(ms >> 32)
	raw[2] = byte(ms >> 24)
	raw[3] = byte(ms >> 16)
	raw[4] = byte(ms >> 8)
	raw[5] = byte(ms)
	if _, err := rand.Read(raw[6:]); err != nil {
		return "", fmt.Errorf("ulid: rand.Read: %w", err)
	}
	return encodeCrockford(raw[:]), nil
}

// encodeCrockford takes 16 bytes (128 bits) and produces 26 Crockford Base32
// chars. The ULID spec defines a specific bit-packing — see https://github.com/ulid/spec.
func encodeCrockford(raw []byte) string {
	if len(raw) != 16 {
		panic("ulid: encodeCrockford expects 16 bytes")
	}
	dst := make([]byte, 26)
	// Timestamp portion (10 chars from first 48 bits).
	dst[0] = crockford[(raw[0]&224)>>5]
	dst[1] = crockford[raw[0]&31]
	dst[2] = crockford[(raw[1]&248)>>3]
	dst[3] = crockford[((raw[1]&7)<<2)|((raw[2]&192)>>6)]
	dst[4] = crockford[(raw[2]&62)>>1]
	dst[5] = crockford[((raw[2]&1)<<4)|((raw[3]&240)>>4)]
	dst[6] = crockford[((raw[3]&15)<<1)|((raw[4]&128)>>7)]
	dst[7] = crockford[(raw[4]&124)>>2]
	dst[8] = crockford[((raw[4]&3)<<3)|((raw[5]&224)>>5)]
	dst[9] = crockford[raw[5]&31]
	// Randomness portion (16 chars from last 80 bits).
	dst[10] = crockford[(raw[6]&248)>>3]
	dst[11] = crockford[((raw[6]&7)<<2)|((raw[7]&192)>>6)]
	dst[12] = crockford[(raw[7]&62)>>1]
	dst[13] = crockford[((raw[7]&1)<<4)|((raw[8]&240)>>4)]
	dst[14] = crockford[((raw[8]&15)<<1)|((raw[9]&128)>>7)]
	dst[15] = crockford[(raw[9]&124)>>2]
	dst[16] = crockford[((raw[9]&3)<<3)|((raw[10]&224)>>5)]
	dst[17] = crockford[raw[10]&31]
	dst[18] = crockford[(raw[11]&248)>>3]
	dst[19] = crockford[((raw[11]&7)<<2)|((raw[12]&192)>>6)]
	dst[20] = crockford[(raw[12]&62)>>1]
	dst[21] = crockford[((raw[12]&1)<<4)|((raw[13]&240)>>4)]
	dst[22] = crockford[((raw[13]&15)<<1)|((raw[14]&128)>>7)]
	dst[23] = crockford[(raw[14]&124)>>2]
	dst[24] = crockford[((raw[14]&3)<<3)|((raw[15]&224)>>5)]
	dst[25] = crockford[raw[15]&31]
	return string(dst)
}

// -----------------------------------------------------------------------------
// Body hash (for dedup)
// -----------------------------------------------------------------------------

// BodyHash returns the SHA-256 of the body normalised for dedup:
// whitespace trimmed at both ends, line endings normalised to "\n",
// trailing whitespace on each line removed.
func (i *Instinct) BodyHash() string {
	return BodyHashOf(i.Body)
}

// BodyHashOf is the standalone version useful for hashing imported bundles.
func BodyHashOf(body string) string {
	normalized := normalizeBody(body)
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

func normalizeBody(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	for j, line := range lines {
		lines[j] = strings.TrimRight(line, " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
