package catalog

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode"

	"modelkeeper/internal/msg"
)

// Tag aliases and groups (G1).

// ResolveTag returns the tag an alias stands for (name itself if it is no alias).
func ResolveTag(ctx context.Context, q queryer, name string) (string, error) {
	var target string
	err := q.QueryRowContext(ctx, `SELECT target FROM tag_alias WHERE alias = ?`, name).Scan(&target)
	if errors.Is(err, sql.ErrNoRows) {
		return name, nil
	}
	return target, err
}

// TagAlias is another spelling of a tag.
type TagAlias struct {
	Alias  string `json:"alias"`
	Target string `json:"target"`
}

// ListTagAliases returns all aliases, sorted by target and alias.
func ListTagAliases(ctx context.Context, q queryer) ([]TagAlias, error) {
	rows, err := q.QueryContext(ctx, `SELECT alias, target FROM tag_alias ORDER BY target COLLATE NOCASE, alias COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TagAlias{}
	for rows.Next() {
		var a TagAlias
		if err := rows.Scan(&a.Alias, &a.Target); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AddTagAlias makes alias another spelling of target. If alias is a tag in
// use, it is merged into target first (undoable, like a merge in the tag
// manager). Returns the number of models whose tag changed.
func AddTagAlias(ctx context.Context, db *sql.DB, alias, target string) (int, error) {
	a, err := cleanTag(alias)
	if err != nil {
		return 0, err
	}
	t, err := cleanTag(target)
	if err != nil {
		return 0, err
	}
	if t2, err := ResolveTag(ctx, db, t); err != nil {
		return 0, err
	} else {
		t = t2 // an alias of an alias points at the tag itself
	}
	if strings.EqualFold(a, t) {
		return 0, msg.Wrap(nil, "tags.aliasSame")
	}
	n := 0
	if _, _, err := tagByName(ctx, db, a); err == nil {
		if n, err = RenameTag(ctx, db, a, t); err != nil {
			return 0, err
		}
	}
	err = inTx(ctx, db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO tag_alias (alias, target, created_at) VALUES (?, ?, ?)
			ON CONFLICT (alias) DO UPDATE SET target = excluded.target`, a, t, time.Now().UnixMilli()); err != nil {
			return err
		}
		// Aliases of the alias now point at the tag.
		_, err := tx.ExecContext(ctx, `UPDATE tag_alias SET target = ? WHERE target = ?`, t, a)
		return err
	})
	return n, err
}

// RemoveTagAlias removes an alias (the tags of models stay as they are).
func RemoveTagAlias(ctx context.Context, db *sql.DB, alias string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM tag_alias WHERE alias = ?`, alias)
	return err
}

// maxGroupLength bounds group names (characters).
const maxGroupLength = 40

// SetTagGroup puts a tag into a group ("" = no group).
func SetTagGroup(ctx context.Context, db *sql.DB, tag, group string) error {
	g := strings.Join(strings.Fields(group), " ")
	if len([]rune(g)) > maxGroupLength {
		return msg.Wrap(nil, "tags.groupInvalid", "max", maxGroupLength)
	}
	t, err := cleanTag(tag)
	if err != nil {
		return err
	}
	if g == "" {
		_, err = db.ExecContext(ctx, `DELETE FROM tag_group WHERE tag = ?`, t)
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO tag_group (tag, grp) VALUES (?, ?)
		ON CONFLICT (tag) DO UPDATE SET grp = excluded.grp`, t, g)
	return err
}

// TagGroup is the group of a tag.
type TagGroup struct {
	Tag   string `json:"tag"`
	Group string `json:"group"`
}

// ListTagGroups returns the group of every tag that has one.
func ListTagGroups(ctx context.Context, q queryer) ([]TagGroup, error) {
	rows, err := q.QueryContext(ctx, `SELECT tag, grp FROM tag_group ORDER BY grp COLLATE NOCASE, tag COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TagGroup{}
	for rows.Next() {
		var g TagGroup
		if err := rows.Scan(&g.Tag, &g.Group); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// renameTagMeta moves aliases and group from one tag name to another.
func renameTagMeta(ctx context.Context, tx *sql.Tx, from, to string, merged bool) error {
	if _, err := tx.ExecContext(ctx, `UPDATE tag_alias SET target = ? WHERE target = ?`, to, from); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM tag_alias WHERE alias = ?`, to); err != nil {
		return err
	}
	if merged { // the target's group wins; it takes the old one only if it has none
		_, err := tx.ExecContext(ctx, `UPDATE OR IGNORE tag_group SET tag = ? WHERE tag = ?`, to, from)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM tag_group WHERE tag = ?`, from)
		return err
	}
	if strings.EqualFold(from, to) {
		_, err := tx.ExecContext(ctx, `UPDATE tag_group SET tag = ? WHERE tag = ?`, to, from)
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE OR REPLACE tag_group SET tag = ? WHERE tag = ?`, to, from)
	return err
}

// SimilarTags is a set of tags that are probably the same (plural, hyphen,
// spacing), to be merged by the user.
type SimilarTags struct {
	Tags []TagCount `json:"tags"` // most used first
}

// tagKey normalizes a tag for finding similar ones: lower case, only letters
// and digits, English and German plural endings removed.
func tagKey(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	k := b.String()
	for _, suf := range []struct{ from, to string }{{"ves", "f"}, {"ies", "y"}, {"es", ""}, {"s", ""}, {"en", ""}, {"n", ""}, {"e", ""}} {
		if len(k) > len(suf.from)+2 && strings.HasSuffix(k, suf.from) {
			return strings.TrimSuffix(k, suf.from) + suf.to
		}
	}
	return k
}

// FindSimilarTags returns groups of tags that look like the same tag.
func FindSimilarTags(ctx context.Context, db *sql.DB) ([]SimilarTags, error) {
	tags, err := ListTags(ctx, db)
	if err != nil {
		return nil, err
	}
	by := map[string][]TagCount{}
	var keys []string
	for _, t := range tags {
		k := tagKey(t.Name)
		if k == "" {
			continue
		}
		if _, ok := by[k]; !ok {
			keys = append(keys, k)
		}
		by[k] = append(by[k], t)
	}
	sort.Strings(keys)
	out := []SimilarTags{}
	for _, k := range keys {
		g := by[k]
		if len(g) < 2 {
			continue
		}
		sort.SliceStable(g, func(i, j int) bool { return g[i].Models > g[j].Models })
		out = append(out, SimilarTags{Tags: g})
	}
	return out, nil
}
