package vanity

import (
	"encoding/json"
	"log/slog"
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

func ReplacementsFromCfg() (replacements []Replacement) {
	data := [][]string{}
	cfgJson, ok := os.LookupEnv("ATHENS_VANITY_CFG")
	if ok {
		dataBytes, err := os.ReadFile(cfgJson)
		if err != nil {
			slog.Error(err.Error())
			return replacements
		}
		if err = json.Unmarshal(dataBytes, &data); err != nil {
			slog.Error(err.Error())
			return replacements
		}
		for idx := range data {
			if len(data[idx]) == 2 {
				replacements = append(replacements, Replacement{
					Vanity:      data[idx][0],
					Replacement: data[idx][1],
				})
			}
		}
	}
	return replacements
}
