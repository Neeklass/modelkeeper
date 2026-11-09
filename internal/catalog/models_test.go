package catalog

import "testing"

func TestFTSQuery(t *testing.T) {
	for in, want := range map[string]string{
		"":                 "",
		"  ":               "",
		"orc war":          `"orc"* "war"*`,
		`"drop" OR (x*)`:   `"drop"* "OR"* "x"*`,
		"Stella, the Moon": `"Stella"* "the"* "Moon"*`,
		"Größe-32mm":       `"Größe"* "32mm"*`,
	} {
		if got := ftsQuery(in); got != want {
			t.Errorf("ftsQuery(%q) = %q, want %q", in, got, want)
		}
	}
}
