package vanity

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/gomods/athens/pkg/vanity/plugins/gopkg"
	"github.com/gorilla/mux"
)

type VanityPlugin interface {
	ReplaceVanity(path string, req *http.Request) (module string, version string, err error)
	RestoreVanity(path string, extras ...interface{}) (string, error)
}

type entry struct {
	vanityModule  string
	vanityVersion string
	replModule    string
	replVersion   string
}

var (
	replacements []Replacement
	plugins      map[string]VanityPlugin
	cache        map[string]*entry
)

type Replacement struct {
	Vanity      string `json:"vanity"`
	Replacement string `json:"repl"`
	Plugin      string `json:"plugin,omitempty"`
}

func init() {
	cfgJson, ok := os.LookupEnv("ATHENS_VANITY_CFG")
	if !ok {
		return
	}

	plugins = make(map[string]VanityPlugin)
	cache = make(map[string]*entry)

	dataBytes, err := os.ReadFile(cfgJson)
	if err != nil {
		panic(err)
	}
	if err = json.Unmarshal(dataBytes, &replacements); err != nil {
		slog.Error(err.Error())
		panic(err)
	}
	for idx := range replacements {
		if replacements[idx].Plugin != "" {
			initializePlugin(replacements[idx].Plugin)
		}
	}
}

func initializePlugin(name string) {
	switch name {
	case "gopkg.in":
		if _, ok := plugins[name]; !ok {
			plugins[name] = gopkg.NewVanity()
		}
	}
}

// func VanityIdx(path string) int {

// }

func ReplaceMod(path string, req *http.Request) string {
	if e, ok := cache[path]; ok {
		return e.replModule
	}
	for idx := range replacements {
		if strings.HasPrefix(path, replacements[idx].Vanity) {
			if replacements[idx].Plugin != "" {
				if plugin, ok := plugins[replacements[idx].Plugin]; ok {
					repl, ver, err := plugin.ReplaceVanity(path, req)
					if err != nil {
						slog.Error("error replacing vanity", slog.String("err", err.Error()))
						return path
					}
					newE := entry{
						vanityModule:  path,
						vanityVersion: mux.Vars(req)["version"],
						replModule:    repl,
						replVersion:   ver,
					}
					cache[path] = &newE
					cache[newE.replModule] = &newE
					return repl
				}
				slog.Error("undefined vanity plugin", slog.String("Plugin", replacements[idx].Plugin))
			}
			newE := entry{
				vanityModule:  path,
				vanityVersion: mux.Vars(req)["version"],
				replModule:    strings.Replace(path, replacements[idx].Vanity, replacements[idx].Replacement, 1),
				replVersion:   mux.Vars(req)["version"],
			}
			cache[path] = &newE
			cache[newE.replModule] = &newE

			return newE.replModule
		}
	}
	return path
}

func Restore(path string) (string, bool) {
	if e, ok := cache[path]; ok {
		return e.vanityModule, true
	}
	return path, false
}
