package instincts

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// ClusterOptions controls the thresholds used by Cluster().
type ClusterOptions struct {
	// MinClusterSize is the minimum number of instincts in a cluster for it
	// to be a draft candidate. Default 3.
	MinClusterSize int

	// MinAvgConfidence is the minimum average confidence across the cluster
	// for it to be a draft candidate. Default 0.6.
	MinAvgConfidence float64

	// SimilarityThreshold is the minimum pairwise jaccard-weighted similarity
	// for two instincts within the same domain to belong to the same cluster.
	// Default 0.5.
	SimilarityThreshold float64
}

// DefaultClusterOptions returns the v1 defaults.
func DefaultClusterOptions() ClusterOptions {
	return ClusterOptions{
		MinClusterSize:      3,
		MinAvgConfidence:    0.6,
		SimilarityThreshold: 0.5,
	}
}

// Cluster is a single output of the clustering algorithm.
type Cluster struct {
	// Domain shared by all instincts in the cluster.
	Domain string

	// Members holds the instincts that ended up in this cluster.
	Members []*Instinct

	// AvgConfidence is the mean of Members' Confidence.
	AvgConfidence float64

	// Keywords is the union of high-frequency title keywords across Members.
	Keywords []string

	// Tags is the union of all Tags across Members.
	Tags []string

	// SkippedReason is set if the cluster was returned but is below thresholds
	// (informational so callers can report). Empty for cluster candidates.
	SkippedReason string
}

// ClusterAll groups instincts by domain, then computes pairwise similarity
// within each domain and forms transitive clusters of similar entries. Returns
// both candidate clusters (ready for draft generation) and skipped clusters
// (below thresholds, included for reporting).
func ClusterAll(items []*Instinct, opts ClusterOptions) (candidates []*Cluster, skipped []*Cluster) {
	if opts.MinClusterSize <= 0 {
		opts.MinClusterSize = 3
	}
	if opts.MinAvgConfidence < 0 {
		opts.MinAvgConfidence = 0.6
	}
	if opts.SimilarityThreshold <= 0 {
		opts.SimilarityThreshold = 0.5
	}

	byDomain := map[string][]*Instinct{}
	for _, it := range items {
		d := strings.TrimSpace(it.Domain)
		if d == "" {
			continue
		}
		byDomain[d] = append(byDomain[d], it)
	}

	var domains []string
	for d := range byDomain {
		domains = append(domains, d)
	}
	sort.Strings(domains)

	for _, domain := range domains {
		group := byDomain[domain]
		clusters := transitiveClusters(group, opts.SimilarityThreshold)
		for _, members := range clusters {
			if len(members) == 0 {
				continue
			}
			c := buildCluster(domain, members)
			if len(c.Members) < opts.MinClusterSize {
				c.SkippedReason = fmt.Sprintf("size %d below threshold %d", len(c.Members), opts.MinClusterSize)
				skipped = append(skipped, c)
				continue
			}
			if c.AvgConfidence < opts.MinAvgConfidence {
				c.SkippedReason = fmt.Sprintf("avg confidence %.2f below threshold %.2f", c.AvgConfidence, opts.MinAvgConfidence)
				skipped = append(skipped, c)
				continue
			}
			candidates = append(candidates, c)
		}
	}
	return candidates, skipped
}

func buildCluster(domain string, members []*Instinct) *Cluster {
	c := &Cluster{Domain: domain, Members: members}
	// Avg confidence.
	if len(members) > 0 {
		sum := 0.0
		for _, m := range members {
			sum += m.Confidence
		}
		c.AvgConfidence = sum / float64(len(members))
	}
	// Keyword + tag unions.
	kwSet := map[string]struct{}{}
	tagSet := map[string]struct{}{}
	for _, m := range members {
		for _, k := range titleKeywords(m.Title) {
			kwSet[k] = struct{}{}
		}
		for _, t := range m.Tags {
			tagSet[strings.ToLower(t)] = struct{}{}
		}
	}
	c.Keywords = sortedKeys(kwSet)
	c.Tags = sortedKeys(tagSet)
	return c
}

// transitiveClusters builds clusters from items where any two items
// connected by similarity >= threshold end up in the same cluster (union-find).
func transitiveClusters(items []*Instinct, threshold float64) [][]*Instinct {
	n := len(items)
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		if parent[i] != i {
			parent[i] = find(parent[i])
		}
		return parent[i]
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}

	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if similarity(items[i], items[j]) >= threshold {
				union(i, j)
			}
		}
	}

	groups := map[int][]*Instinct{}
	for i, it := range items {
		groups[find(i)] = append(groups[find(i)], it)
	}
	out := make([][]*Instinct, 0, len(groups))
	for _, g := range groups {
		out = append(out, g)
	}
	// Deterministic order: sort groups by their first member's ID.
	sort.Slice(out, func(i, j int) bool {
		return out[i][0].ID < out[j][0].ID
	})
	return out
}

// similarity is 0.4 * jaccard(tags) + 0.6 * jaccard(title keywords).
func similarity(a, b *Instinct) float64 {
	tagSim := jaccardStrings(a.Tags, b.Tags)
	kwSim := jaccardStrings(titleKeywords(a.Title), titleKeywords(b.Title))
	return 0.4*tagSim + 0.6*kwSim
}

func jaccardStrings(a, b []string) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	setA := map[string]struct{}{}
	setB := map[string]struct{}{}
	for _, s := range a {
		setA[strings.ToLower(s)] = struct{}{}
	}
	for _, s := range b {
		setB[strings.ToLower(s)] = struct{}{}
	}
	intersection := 0
	for k := range setA {
		if _, ok := setB[k]; ok {
			intersection++
		}
	}
	union := len(setA) + len(setB) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// -----------------------------------------------------------------------------
// Keyword extraction
// -----------------------------------------------------------------------------

var nonAlpha = regexp.MustCompile(`[^\p{L}\p{N}]+`)

// titleKeywords splits a title into lowercase tokens ≥4 chars, excluding stopwords.
func titleKeywords(title string) []string {
	tokens := nonAlpha.Split(strings.ToLower(title), -1)
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if len(t) < 4 {
			continue
		}
		if _, isStop := stopwords[t]; isStop {
			continue
		}
		out = append(out, t)
	}
	return out
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// stopwords is a hardcoded set of common English + Spanish words to ignore
// when extracting keywords from titles. Inclusion is intentionally conservative;
// false-positive filtering (a real keyword being dropped) is preferred over
// noisy clusters dominated by glue words.
var stopwords = map[string]struct{}{
	// English (high-frequency stopwords ≥4 chars)
	"about": {}, "after": {}, "again": {}, "also": {}, "always": {},
	"because": {}, "been": {}, "being": {}, "before": {}, "both": {},
	"could": {}, "does": {}, "doing": {}, "down": {}, "during": {},
	"each": {}, "ever": {}, "every": {}, "from": {}, "have": {},
	"having": {}, "here": {}, "into": {}, "just": {}, "like": {},
	"made": {}, "make": {}, "many": {}, "more": {}, "most": {},
	"much": {}, "must": {}, "need": {}, "never": {}, "once": {},
	"only": {}, "other": {}, "over": {}, "same": {}, "should": {},
	"some": {}, "such": {}, "take": {}, "than": {}, "that": {},
	"them": {}, "then": {}, "there": {}, "they": {}, "thing": {},
	"this": {}, "those": {}, "through": {}, "very": {}, "well": {},
	"were": {}, "what": {}, "when": {}, "where": {}, "which": {},
	"while": {}, "will": {}, "with": {}, "would": {}, "your": {},
	// Spanish (high-frequency stopwords ≥4 chars)
	"acerca": {}, "ahora": {}, "algun": {}, "alguna": {}, "algunas": {},
	"alguno": {}, "algunos": {}, "ante": {}, "antes": {}, "aqui": {},
	"bajo": {}, "bien": {}, "casi": {}, "cada": {}, "como": {},
	"cuando": {}, "cuyo": {}, "desde": {}, "donde": {}, "ellas": {},
	"ellos": {}, "entre": {}, "esta": {}, "estas": {}, "este": {},
	"estos": {}, "estoy": {}, "fuera": {}, "hace": {}, "hacer": {},
	"hacia": {}, "hasta": {}, "luego": {}, "mientras": {}, "mismo": {},
	"mucha": {}, "mucho": {}, "muchas": {}, "muchos": {}, "nada": {},
	"nadie": {}, "nuestro": {}, "nunca": {}, "otra": {}, "otras": {},
	"otro": {}, "otros": {}, "para": {}, "pero": {}, "poco": {},
	"porque": {}, "puede": {}, "puedo": {}, "quien": {}, "quienes": {},
	"siempre": {}, "sino": {}, "sobre": {}, "tambien": {}, "tampoco": {},
	"tanto": {}, "tener": {}, "tenia": {}, "tiene": {}, "todas": {},
	"todo": {}, "todos": {}, "unas": {}, "unos": {}, "usted": {},
}
