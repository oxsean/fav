// Package skills embeds the /fav skill so a release binary can install it.
package skills

import _ "embed"

//go:embed fav/SKILL.md
var Fav []byte
