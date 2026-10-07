// Package schemas embeds the JSON schemas for structured model output.
package schemas

import _ "embed"

// ArmResult is the final answer every baseline arm must give (contracts/arms.md).
//
//go:embed arm_result.json
var ArmResult []byte
