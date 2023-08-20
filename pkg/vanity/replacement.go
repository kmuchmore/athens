package vanity

import (
	"os"
	"strings"
)

type Replacement struct {
	Vanity      string
	Replacement string
}

func ReplacementsFromEnv() []Replacement {
	replacements := []Replacement{}
	replacementsStr, ok := os.LookupEnv("ATHENS_VANITY_REPL")
	if ok {
		replArr := strings.Split(replacementsStr, ",")
		if len(replArr)%2 != 0 {
			return replacements
		}
		for i := 1; i < len(replArr); i += 2 {
			replacements = append(replacements, Replacement{
				Vanity:      replArr[i-1],
				Replacement: replArr[i],
			})
		}
	}
	return replacements
}
