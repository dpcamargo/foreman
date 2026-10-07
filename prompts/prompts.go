// Package prompts embeds versioned prompt templates. Each run records a hash of the
// template it used, so prompt changes are attributable (constitution: continuous eval).
package prompts

import _ "embed"

// ArmBaseline is the prompt every baseline arm gets (contracts/arms.md, "Prompt").
// Placeholders: {{REQUEST}} and {{VISIBLE_TESTS}}.
//
//go:embed arm_baseline.md
var ArmBaseline string
