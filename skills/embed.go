// Package skills embeds the /tend skill so a release binary can install it.
package skills

import _ "embed"

//go:embed tend/SKILL.md
var Tend []byte
