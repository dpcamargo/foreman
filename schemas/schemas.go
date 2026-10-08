// Package schemas embeds the JSON schemas for structured model output.
package schemas

import _ "embed"

// ArmResult is the final answer every baseline arm must give (contracts/arms.md).
//
//go:embed arm_result.json
var ArmResult []byte

// TaskSpec is the structured output from the specify step in Feature 002 (FR-004).
//
//go:embed task_spec.json
var TaskSpec []byte

// ReviewFindings is the structured output from the cross-family review step in Feature 002 (FR-007).
//
//go:embed review_findings.json
var ReviewFindings []byte
