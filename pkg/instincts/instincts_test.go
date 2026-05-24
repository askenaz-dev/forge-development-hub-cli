package instincts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// -----------------------------------------------------------------------------
// ULID
// -----------------------------------------------------------------------------

func TestNewULID_Format(t *testing.T) {
	id, err := NewULID(time.Now())
	if err != nil {
		t.Fatalf("NewULID error: %v", err)
	}
	if len(id) != 26 {
		t.Fatalf("ULID length = %d, want 26", len(id))
	}
	if !ulidPattern.MatchString(id) {
		t.Fatalf("ULID %q does not match pattern", id)
	}
}

func TestNewULID_Ordering(t *testing.T) {
	a, _ := NewULID(time.Unix(1_700_000_000, 0))
	b, _ := NewULID(time.Unix(1_800_000_000, 0))
	if !(a < b) {
		t.Fatalf("expected a < b (chronological→lexicographical); got a=%q b=%q", a, b)
	}
}

func TestNewULID_RejectsPreEpoch(t *testing.T) {
	_, err := NewULID(time.Unix(-1, 0))
	if err == nil {
		t.Fatalf("expected error for pre-epoch timestamp")
	}
}

// -----------------------------------------------------------------------------
// Validation
// -----------------------------------------------------------------------------

func validInstinct(t *testing.T) *Instinct {
	t.Helper()
	id, err := NewULID(time.Now())
	if err != nil {
		t.Fatalf("NewULID: %v", err)
	}
	return &Instinct{
		ID:         id,
		Title:      "When refactoring services, verify OTel trace IDs",
		Confidence: 0.7,
		Domain:     "backend-services-go",
		CapturedBy: "dev@forge.com",
		CapturedAt: time.Now().UTC(),
		Context:    Context{ProjectHint: "checkout-service"},
		Tags:       []string{"go", "observability", "refactor"},
		Body:       "Always check OTel trace IDs after a refactor.\n",
	}
}

func TestInstinct_Validate_Valid(t *testing.T) {
	i := validInstinct(t)
	if err := i.Validate(); err != nil {
		t.Fatalf("valid instinct returned err: %v", err)
	}
}

func TestInstinct_Validate_RejectsBadID(t *testing.T) {
	i := validInstinct(t)
	i.ID = "not-a-ulid"
	if err := i.Validate(); err == nil {
		t.Fatal("expected error for bad ULID")
	}
}

func TestInstinct_Validate_RejectsConfidenceOutOfRange(t *testing.T) {
	for _, c := range []float64{-0.1, 1.1, 99.0} {
		i := validInstinct(t)
		i.Confidence = c
		if err := i.Validate(); err == nil {
			t.Fatalf("expected error for confidence=%.2f", c)
		}
	}
}

func TestInstinct_Validate_RejectsEmptyTitle(t *testing.T) {
	i := validInstinct(t)
	i.Title = "   "
	if err := i.Validate(); err == nil {
		t.Fatal("expected error for empty title")
	}
}

func TestInstinct_Validate_RejectsLongTitle(t *testing.T) {
	i := validInstinct(t)
	i.Title = strings.Repeat("x", 121)
	if err := i.Validate(); err == nil {
		t.Fatal("expected error for >120 char title")
	}
}

func TestInstinct_Validate_RejectsEmptyDomain(t *testing.T) {
	i := validInstinct(t)
	i.Domain = ""
	if err := i.Validate(); err == nil {
		t.Fatal("expected error for empty domain")
	}
}

func TestInstinct_Validate_RejectsEmptyBody(t *testing.T) {
	i := validInstinct(t)
	i.Body = "  \n  "
	if err := i.Validate(); err == nil {
		t.Fatal("expected error for empty body")
	}
}

// -----------------------------------------------------------------------------
// Encode / Decode roundtrip
// -----------------------------------------------------------------------------

func TestEncodeDecodeRoundtrip(t *testing.T) {
	original := validInstinct(t)
	original.Body = "Body line 1.\n\nBody line 2."

	encoded, err := original.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !strings.HasPrefix(string(encoded), "---\n") {
		t.Fatal("encoded content should start with frontmatter delimiter")
	}

	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded.ID != original.ID {
		t.Errorf("id mismatch: got %q want %q", decoded.ID, original.ID)
	}
	if decoded.Title != original.Title {
		t.Errorf("title mismatch: got %q want %q", decoded.Title, original.Title)
	}
	if decoded.Confidence != original.Confidence {
		t.Errorf("confidence mismatch: %v vs %v", decoded.Confidence, original.Confidence)
	}
	if decoded.Domain != original.Domain {
		t.Errorf("domain mismatch")
	}
	if !strings.Contains(decoded.Body, "Body line 1.") {
		t.Errorf("body lost: %q", decoded.Body)
	}
}

func TestDecode_MissingFrontmatter(t *testing.T) {
	_, err := Decode([]byte("just markdown\n"))
	if err == nil {
		t.Fatal("expected error for missing frontmatter")
	}
}

// -----------------------------------------------------------------------------
// BodyHash for dedup
// -----------------------------------------------------------------------------

func TestBodyHash_NormalizesWhitespace(t *testing.T) {
	cases := []string{
		"hello world\n",
		"hello world",
		"  hello world  ",
		"hello world\r\n",
		"hello world   \n",
		"\n\nhello world\n\n",
	}
	first := BodyHashOf(cases[0])
	for _, c := range cases {
		if BodyHashOf(c) != first {
			t.Errorf("hash differs for %q (expected normalized equality)", c)
		}
	}
}

func TestBodyHash_DistinguishesContent(t *testing.T) {
	if BodyHashOf("hello") == BodyHashOf("world") {
		t.Fatal("hash of different content should differ")
	}
}

// -----------------------------------------------------------------------------
// Storage with FDH_HOME temp dir
// -----------------------------------------------------------------------------

func withTempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("FDH_HOME", dir)
	return dir
}

func TestEnsureDir_CreatesWithPerms(t *testing.T) {
	withTempHome(t)
	dir, err := EnsureDir()
	if err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("instincts dir not created: %v", err)
	}
}

func TestWriteAtomic_AndRead(t *testing.T) {
	withTempHome(t)
	i := validInstinct(t)
	if err := WriteAtomic(i); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	loaded, err := Read(i.ID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if loaded.Title != i.Title {
		t.Fatalf("roundtrip title mismatch: %q vs %q", loaded.Title, i.Title)
	}
}

func TestList_OnlyValidULIDs(t *testing.T) {
	home := withTempHome(t)
	good := validInstinct(t)
	if err := WriteAtomic(good); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	// Add some junk into the instincts dir.
	dir := filepath.Join(home, "instincts")
	_ = os.WriteFile(filepath.Join(dir, "not-a-ulid.yaml"), []byte("junk"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "stray.tmp"), []byte("junk"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "README"), []byte("junk"), 0o600)

	ids, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ids) != 1 || ids[0] != good.ID {
		t.Fatalf("List returned unexpected IDs: %v", ids)
	}
	// .tmp should have been cleaned up
	if _, err := os.Stat(filepath.Join(dir, "stray.tmp")); !os.IsNotExist(err) {
		t.Fatal("expected stray.tmp to be cleaned up")
	}
}

func TestResolvePrefix(t *testing.T) {
	withTempHome(t)
	a := validInstinct(t)
	a.ID = "01HXY7K2QZ3M5R9TPVNBJ8D6F4"
	a.CapturedAt = time.Now().UTC()
	b := validInstinct(t)
	b.ID = "01HXY7K2QZ3M5R9TPVNBJ8DX9Y" // 26 chars, sorts after a
	b.CapturedAt = time.Now().UTC()
	if err := WriteAtomic(a); err != nil {
		t.Fatalf("Write a: %v", err)
	}
	if err := WriteAtomic(b); err != nil {
		t.Fatalf("Write b: %v", err)
	}

	// Unique prefix
	id, err := ResolvePrefix("01HXY7K2QZ3M5R9TPVNBJ8D6")
	if err != nil || id != a.ID {
		t.Fatalf("unique prefix expected %s, got %q err=%v", a.ID, id, err)
	}

	// Ambiguous prefix
	if _, err := ResolvePrefix("01HXY"); err == nil {
		t.Fatal("expected ambiguous prefix error")
	}

	// No match
	if _, err := ResolvePrefix("ZZZZZ"); err == nil {
		t.Fatal("expected no-match error")
	}
}

// -----------------------------------------------------------------------------
// state.json integration
// -----------------------------------------------------------------------------

func TestMutateState_CreatesAndUpdates(t *testing.T) {
	withTempHome(t)
	// First call creates the file.
	now := time.Now().UTC()
	err := MutateState(func(s *StateInstincts) {
		s.Count = 1
		s.LastCapture = &now
	})
	if err != nil {
		t.Fatalf("MutateState: %v", err)
	}
	got, err := ReadStateInstincts()
	if err != nil {
		t.Fatalf("ReadStateInstincts: %v", err)
	}
	if got.Count != 1 {
		t.Errorf("count=%d want 1", got.Count)
	}
	// Second call increments.
	err = MutateState(func(s *StateInstincts) {
		s.Count++
		s.EvolveRuns = 5
	})
	if err != nil {
		t.Fatalf("MutateState 2: %v", err)
	}
	got, _ = ReadStateInstincts()
	if got.Count != 2 || got.EvolveRuns != 5 {
		t.Errorf("after 2nd mutate: %+v", got)
	}
}

func TestMutateState_PreservesOtherKeys(t *testing.T) {
	withTempHome(t)
	statePath, _ := HomeDir()
	statePath = filepath.Join(statePath, "state.json")
	_ = os.MkdirAll(filepath.Dir(statePath), 0o700)
	if err := os.WriteFile(statePath, []byte(`{
		"schema_version": 1,
		"user_scope_installs": {"skills": []},
		"hub_cache": {"commit": "abc123"}
	}`), 0o600); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	err := MutateState(func(s *StateInstincts) {
		s.Count = 7
	})
	if err != nil {
		t.Fatalf("MutateState: %v", err)
	}
	data, _ := os.ReadFile(statePath)
	for _, must := range []string{`"schema_version"`, `"user_scope_installs"`, `"hub_cache"`, `"abc123"`, `"instincts"`} {
		if !strings.Contains(string(data), must) {
			t.Errorf("state.json missing %s after mutate; got:\n%s", must, data)
		}
	}
}

// -----------------------------------------------------------------------------
// Clustering
// -----------------------------------------------------------------------------

func sampleCluster(t *testing.T, n int, domain string) []*Instinct {
	t.Helper()
	items := make([]*Instinct, n)
	for i := 0; i < n; i++ {
		it := validInstinct(t)
		it.Domain = domain
		it.Title = "When refactoring services trace something specific"
		it.Tags = []string{"go", "trace", "refactor"}
		it.Confidence = 0.8
		items[i] = it
	}
	return items
}

func TestCluster_BasicSameDomainSimilarTags(t *testing.T) {
	withTempHome(t)
	items := sampleCluster(t, 4, "backend-services-go")
	candidates, _ := ClusterAll(items, DefaultClusterOptions())
	if len(candidates) != 1 {
		t.Fatalf("expected 1 cluster candidate, got %d", len(candidates))
	}
	if candidates[0].Domain != "backend-services-go" {
		t.Errorf("unexpected domain %s", candidates[0].Domain)
	}
}

func TestCluster_SkipsSmallClusters(t *testing.T) {
	withTempHome(t)
	items := sampleCluster(t, 2, "tiny-domain")
	candidates, skipped := ClusterAll(items, DefaultClusterOptions())
	if len(candidates) != 0 {
		t.Fatal("expected zero candidates for cluster smaller than min_size")
	}
	if len(skipped) == 0 {
		t.Fatal("expected at least one skipped cluster reported")
	}
}

func TestCluster_SkipsLowConfidence(t *testing.T) {
	withTempHome(t)
	items := sampleCluster(t, 4, "low-conf")
	for _, it := range items {
		it.Confidence = 0.3
	}
	candidates, skipped := ClusterAll(items, DefaultClusterOptions())
	if len(candidates) != 0 {
		t.Fatal("expected zero candidates with avg confidence below threshold")
	}
	if len(skipped) == 0 {
		t.Fatal("expected skipped cluster reported")
	}
}

func TestCluster_CrossDomainStaysSeparate(t *testing.T) {
	withTempHome(t)
	a := sampleCluster(t, 3, "domain-a")
	b := sampleCluster(t, 3, "domain-b")
	combined := append(a, b...)
	candidates, _ := ClusterAll(combined, DefaultClusterOptions())
	if len(candidates) != 2 {
		t.Fatalf("expected 2 separate clusters (one per domain), got %d", len(candidates))
	}
}

// -----------------------------------------------------------------------------
// Draft rendering
// -----------------------------------------------------------------------------

func TestRenderDraft_ContainsBannerAndSources(t *testing.T) {
	items := sampleCluster(t, 3, "backend")
	clusters, _ := ClusterAll(items, DefaultClusterOptions())
	if len(clusters) == 0 {
		t.Fatal("expected 1 cluster")
	}
	out := clusters[0].RenderDraft(DraftOptions{
		GeneratedAt:   time.Now(),
		EvolveCommand: "fdh evolve",
	})
	if !strings.HasPrefix(out, DraftBannerPrefix) {
		t.Errorf("draft does not start with banner: %s", out[:80])
	}
	if !strings.Contains(out, "## Sourced from") {
		t.Error("draft missing Sourced from section")
	}
	for _, it := range items {
		if !strings.Contains(out, it.ID) {
			t.Errorf("draft missing source ID %s", it.ID)
		}
	}
}

func TestSlug_Deterministic(t *testing.T) {
	items := sampleCluster(t, 3, "backend-services-go")
	c1, _ := ClusterAll(items, DefaultClusterOptions())
	c2, _ := ClusterAll(items, DefaultClusterOptions())
	if len(c1) == 0 || len(c2) == 0 {
		t.Fatal("expected clusters")
	}
	if c1[0].Slug() != c2[0].Slug() {
		t.Errorf("slug not deterministic: %s vs %s", c1[0].Slug(), c2[0].Slug())
	}
}

func TestHasDraftBanner(t *testing.T) {
	if !HasDraftBanner("> ⚠️ DRAFT — generated...") {
		t.Error("HasDraftBanner missed banner")
	}
	if HasDraftBanner("# A normal skill\n\nNo banner here.") {
		t.Error("HasDraftBanner false-positive on normal content")
	}
}

// -----------------------------------------------------------------------------
// Keyword extraction
// -----------------------------------------------------------------------------

func TestTitleKeywords_FiltersStopwordsAndShort(t *testing.T) {
	kws := titleKeywords("When refactoring services we always check trace ids")
	for _, kw := range kws {
		if len(kw) < 4 {
			t.Errorf("keyword too short: %q", kw)
		}
		if _, isStop := stopwords[kw]; isStop {
			t.Errorf("stopword not filtered: %q", kw)
		}
	}
	// Should keep substantive words.
	hasRefactoring := false
	for _, kw := range kws {
		if kw == "refactoring" {
			hasRefactoring = true
		}
	}
	if !hasRefactoring {
		t.Errorf("expected 'refactoring' in %v", kws)
	}
}

func TestTitleKeywords_HandlesSpanish(t *testing.T) {
	kws := titleKeywords("Al refactorizar services siempre verificar trazas")
	if len(kws) == 0 {
		t.Fatal("expected at least one keyword")
	}
	// "siempre" is a Spanish stopword — must be filtered.
	for _, kw := range kws {
		if kw == "siempre" {
			t.Errorf("Spanish stopword 'siempre' not filtered: %v", kws)
		}
	}
}
