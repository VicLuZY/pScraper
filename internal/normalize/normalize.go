package normalize

import (
	"regexp"
	"strings"
)

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)
var space = regexp.MustCompile(`\s+`)

func Text(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "&", " and ")
	s = nonAlnum.ReplaceAllString(s, " ")
	s = space.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func Compact(s string) string {
	return strings.ReplaceAll(Text(s), " ", "")
}

func Address(s string) string {
	s = Text(s)
	repl := map[string]string{
		" street ":    " st ",
		" avenue ":    " ave ",
		" road ":      " rd ",
		" drive ":     " dr ",
		" boulevard ": " blvd ",
		" place ":     " pl ",
		" court ":     " ct ",
		" crescent ":  " cres ",
		" highway ":   " hwy ",
		" north ":     " n ",
		" south ":     " s ",
		" east ":      " e ",
		" west ":      " w ",
	}
	s = " " + s + " "
	for from, to := range repl {
		s = strings.ReplaceAll(s, from, to)
	}
	return strings.TrimSpace(s)
}

func SourceID(s string) string {
	return Compact(s)
}
