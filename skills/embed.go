// Package skills carries the loopgate Claude Code skill inside the binaries.
package skills

import _ "embed"

//go:embed loopgate/SKILL.md
var Loopgate []byte
