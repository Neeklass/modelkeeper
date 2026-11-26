package catalog

import (
	"context"
	"strings"
	"testing"
)

func TestTagAliasesAndGroups(t *testing.T) {
	ctx := context.Background()
	conn, _ := testCatalog(t, "C/Orc/o.stl", "C/Elf/e.stl")
	orc, elf := modelID(t, conn, "Orc"), modelID(t, conn, "Elf")
	if err := AddTags(ctx, conn, []int64{orc}, []string{"elves"}); err != nil {
		t.Fatal(err)
	}
	if err := AddTags(ctx, conn, []int64{elf}, []string{"elf"}); err != nil {
		t.Fatal(err)
	}
	if err := SetTagGroup(ctx, conn, "elf", "Race"); err != nil {
		t.Fatal(err)
	}
	// "elves" becomes an alias of "elf": the tag in use is merged into it.
	n, err := AddTagAlias(ctx, conn, "elves", "elf")
	if err != nil || n != 1 {
		t.Fatalf("alias: %d %v", n, err)
	}
	if d, _ := GetModel(ctx, conn, orc); strings.Join(d.Tags, ",") != "elf" {
		t.Errorf("orc tags %v", d.Tags)
	}
	// Adding the alias adds the tag.
	if err := AddTags(ctx, conn, []int64{elf, orc}, []string{"Elves"}); err != nil {
		t.Fatal(err)
	}
	tags, _ := ListTags(ctx, conn)
	if len(tags) != 1 || tags[0].Name != "elf" || tags[0].Models != 2 || tags[0].Group != "Race" ||
		strings.Join(tags[0].Aliases, ",") != "elves" {
		t.Errorf("tags %+v", tags)
	}
	// A rename takes aliases and group along.
	if _, err := RenameTag(ctx, conn, "elf", "Elf folk"); err != nil {
		t.Fatal(err)
	}
	tags, _ = ListTags(ctx, conn)
	if tags[0].Name != "Elf folk" || tags[0].Group != "Race" || len(tags[0].Aliases) != 1 {
		t.Errorf("after rename %+v", tags)
	}
	if _, err := AddTagAlias(ctx, conn, "elf folk", "Elf folk"); err == nil {
		t.Error("alias of itself accepted")
	}
	if err := RemoveTagAlias(ctx, conn, "elves"); err != nil {
		t.Fatal(err)
	}
	if err := SetTagGroup(ctx, conn, "Elf folk", ""); err != nil {
		t.Fatal(err)
	}
	tags, _ = ListTags(ctx, conn)
	if tags[0].Group != "" || len(tags[0].Aliases) != 0 {
		t.Errorf("after removal %+v", tags)
	}
}

func TestFindSimilarTags(t *testing.T) {
	ctx := context.Background()
	conn, _ := testCatalog(t, "C/Orc/o.stl")
	orc := modelID(t, conn, "Orc")
	AddTags(ctx, conn, []int64{orc}, []string{"Orc", "orcs", "sci-fi", "Sci Fi", "dragon"})
	sim, err := FindSimilarTags(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range sim {
		var names []string
		for _, t := range s.Tags {
			names = append(names, t.Name)
		}
		got = append(got, strings.Join(names, "+"))
	}
	if strings.Join(got, " ") != "Orc+orcs Sci Fi+sci-fi" {
		t.Errorf("similar %v", got)
	}
}
