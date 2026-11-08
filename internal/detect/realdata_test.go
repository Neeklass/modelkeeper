package detect

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"modelkeeper/internal/db"
	"modelkeeper/internal/settings"
)

// TestRealData runs detection on a catalog copy: MODELKEEPER_DEBUG_DB=path (root id 1).
func TestRealData(t *testing.T) {
	p := os.Getenv("MODELKEEPER_DEBUG_DB")
	if p == "" {
		t.Skip()
	}
	conn, err := db.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(conn)
	d := settings.Defaults()
	entries, err := loadEntries(context.Background(), conn, 1)
	if err != nil {
		t.Fatal(err)
	}
	cands := Detect(entries, Rules{CreatorLevel: true, PartNames: d.PartNames, SizeTokens: d.SizeTokens})
	creators := map[string]int{}
	kinds := map[string]int{}
	sizes := map[int]int{}
	for _, c := range cands {
		creators[c.Creator]++
		kinds[c.Kind]++
		n := 0
		for _, a := range c.Assign {
			if a.Content {
				n++
			}
		}
		switch {
		case n <= 1:
			sizes[1]++
		case n <= 5:
			sizes[5]++
		case n <= 20:
			sizes[20]++
		default:
			sizes[1000]++
		}
	}
	fmt.Printf("entries=%d candidates=%d kinds=%v contentFiles<=1:%d <=5:%d <=20:%d more:%d creators=%d\n",
		len(entries), len(cands), kinds, sizes[1], sizes[5], sizes[20], sizes[1000], len(creators))
	type kv struct {
		k string
		v int
	}
	var cs []kv
	for k, v := range creators {
		cs = append(cs, kv{k, v})
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].v > cs[j].v })
	for i, c := range cs {
		if i >= 15 {
			break
		}
		fmt.Printf("  creator %-40q %d\n", c.k, c.v)
	}
	filter := os.Getenv("MODELKEEPER_DEBUG_FILTER")
	step := max(1, len(cands)/60)
	if filter != "" {
		step = 1
	}
	for i := 0; i < len(cands); i += step {
		c := cands[i]
		if filter != "" && !strings.HasPrefix(c.Anchor, filter) {
			continue
		}
		vs := map[string]bool{}
		n := 0
		for _, a := range c.Assign {
			if a.Variant != "" {
				vs[a.Variant] = true
			}
			if a.Content {
				n++
			}
		}
		var vl []string
		for v := range vs {
			vl = append(vl, v)
		}
		sort.Strings(vl)
		fmt.Printf("%-7s files=%-3d sup=%-12s var=%-10s tags=%q\n        %s\n", c.Kind, n, c.Supports,
			strings.Join(vl, ","), c.Tags, c.Anchor)
	}
}
