package main

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Every message key the Go code and the frontend use has a German and an
// English text, and both languages have the same keys (otherwise the user
// sees a raw key like "import.noFile").

func flatKeys(t *testing.T, file string) map[string]bool {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	out := map[string]bool{}
	var walk func(prefix string, v map[string]any)
	walk = func(prefix string, v map[string]any) {
		for k, x := range v {
			if sub, ok := x.(map[string]any); ok {
				walk(prefix+k+".", sub)
			} else {
				out[prefix+k] = true
			}
		}
	}
	walk("", m)
	return out
}

func hasKey(keys map[string]bool, k string) bool {
	return keys[k] || keys[k+"_one"] || keys[k+"_other"]
}

var (
	goKeyRes = []*regexp.Regexp{
		regexp.MustCompile(`msg\.Wrap\([^,()]*(?:\([^)]*\))?,\s*"([a-zA-Z]\w*(?:\.\w+)+)"`),
		regexp.MustCompile(`msg\.New\(\s*"([a-zA-Z]\w*(?:\.\w+)+)"`),
		regexp.MustCompile(`"(history\.[\w.]*\w)"`),
	}
	correctionRe = regexp.MustCompile(`CorrectionPrefix\+"(\w+)"`)
	tsKeyRe      = regexp.MustCompile(`\bt\(\s*'([a-zA-Z]\w*(?:\.\w+)+)'`)
)

func TestTranslationsComplete(t *testing.T) {
	en := flatKeys(t, "frontend/src/i18n/locales/en.json")
	de := flatKeys(t, "frontend/src/i18n/locales/de.json")
	for k := range en {
		if !de[k] {
			t.Errorf("only in en.json: %s", k)
		}
	}
	for k := range de {
		if !en[k] {
			t.Errorf("only in de.json: %s", k)
		}
	}

	used := map[string]string{"warning.dataDir.downloads": "app_userdata.go", "warning.dataDir.synced": "app_userdata.go"}
	filepath.WalkDir(".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", ".git", "dist", "wailsjs", "build":
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		isGo := strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
		isTS := strings.HasSuffix(name, ".ts") || strings.HasSuffix(name, ".tsx")
		if !isGo && !isTS {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		s := string(b)
		if isGo {
			for _, re := range goKeyRes {
				for _, m := range re.FindAllStringSubmatch(s, -1) {
					used[m[1]] = p
				}
			}
			for _, m := range correctionRe.FindAllStringSubmatch(s, -1) {
				used["history.correction."+m[1]] = p
			}
		} else {
			for _, m := range tsKeyRe.FindAllStringSubmatch(s, -1) {
				used[m[1]] = p
			}
		}
		return nil
	})
	var missing []string
	for k, where := range used {
		if !hasKey(en, k) {
			missing = append(missing, k+" ("+where+")")
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("no translation for %s", m)
	}
	if len(used) < 100 {
		t.Errorf("only %d keys found – the patterns no longer match the code", len(used))
	}
}
