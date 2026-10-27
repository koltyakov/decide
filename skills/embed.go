// Package skills embeds agent instructions in the standalone binary.
package skills

import _ "embed"

//go:embed decide/SKILL.md
var Decide []byte
