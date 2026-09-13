// Package i18n: texts live in locales/<lang>.json by semantic key; a missing key falls back to English, then to the key itself.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

//go:embed locales/*.json
var locales embed.FS

var tables = map[string]map[string]string{}

func init() {
	for _, l := range []string{EN, ZH} {
		b, err := locales.ReadFile("locales/" + l + ".json")
		if err != nil {
			panic(err)
		}
		t := map[string]string{}
		if err := json.Unmarshal(b, &t); err != nil {
			panic("i18n " + l + ".json: " + err.Error())
		}
		tables[l] = t
	}
}

const (
	ZH = "zh"
	EN = "en"
)

var lang = ZH

// empty or unknown → zh
func Set(l string) {
	if l == EN {
		lang = EN
		return
	}
	lang = ZH
}

func Lang() string { return lang }

// "" follows the system
func Resolve(cfg string) string {
	if cfg == ZH || cfg == EN {
		return cfg
	}
	return Detect()
}

// LC_ALL / LC_MESSAGES / LANG starting with zh → Chinese, anything else → English; none set → platform default
func Detect() string {
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		v := strings.ToLower(os.Getenv(k))
		if v == "" || v == "c" || v == "posix" {
			continue
		}
		if strings.HasPrefix(v, "zh") {
			return ZH
		}
		return EN
	}
	return detectPlatform()
}

func F(key string, a ...any) string { return fmt.Sprintf(T(key), a...) }

func E(key string, a ...any) error { return fmt.Errorf(T(key), a...) }

func T(key string) string {
	if s, ok := tables[lang][key]; ok {
		return s
	}
	if s, ok := tables[EN][key]; ok {
		return s
	}
	return key
}
