package catalog

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"modelkeeper/internal/db"
)

// TestRealDataSearch: MODELKEEPER_DEBUG_DB=<catalog copy>, MODELKEEPER_DEBUG_QUERY=<comma-free text>.
func TestRealDataSearch(t *testing.T) {
	p := os.Getenv("MODELKEEPER_DEBUG_DB")
	if p == "" {
		t.Skip()
	}
	conn, err := db.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(conn)
	ctx := context.Background()
	for name, fq := range map[string]ModelQuery{
		"duplicates": {Duplicates: true}, "singleLocation": {SingleLocation: true},
		"extractedAndArchive": {ExtractedAndArchive: true}, "brokenArchives": {BrokenArchives: true},
		"ext stl": {Ext: "stl"}, "untagged": {Untagged: true}, "missing": {Missing: true},
	} {
		start := time.Now()
		page, err := SearchModels(ctx, conn, fq, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("filter %-20s %5d models in %v\n", name, page.Total, time.Since(start))
	}
	for _, q := range []string{"", os.Getenv("MODELKEEPER_DEBUG_QUERY")} {
		start := time.Now()
		page, err := SearchModels(ctx, conn, ModelQuery{Text: q}, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("query %q: %d models in %v\n", q, page.Total, time.Since(start))
		for i, r := range page.Items {
			if i >= 5 {
				break
			}
			fmt.Printf("  %-45s %-25s files=%-3d var=%v sup=%s\n", r.Name, r.Creator, r.Files, r.Variants, r.Supports)
		}
		if q != "" && len(page.Items) > 0 {
			start = time.Now()
			d, err := GetModel(ctx, conn, page.Items[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			fmt.Printf("  detail in %v: anchors=%d files=%d suggestions=%v\n", time.Since(start), len(d.Anchors), len(d.Files), d.TagSuggested)
		}
	}
}
