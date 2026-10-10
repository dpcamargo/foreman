// Package policy loads and validates lane definitions from policy.yaml (FR-009).
// Pure parsing and validation — no I/O beyond reading the config file.
package policy

import (
	"fmt"
	"os"
	"strings"
)

// Lane defines budget caps and gating for one task lane (FR-009).
type Lane struct {
	Name         string // "F", "S", "D"
	MaxUSD       float64
	MaxMinutes   int
	Review       string // "light", "full"
	TestFirst    bool
	Deliberation bool
}

// Policy is the full policy.yaml configuration (FR-009).
type Policy struct {
	Lanes                         map[string]Lane
	APISpendWithoutApprovalPerDay float64
	ClaudeSubscriptionUnattended  bool
}

// LoadPolicy reads and validates policy.yaml. Returns error if any referenced
// lane is missing or malformed (FR-009: "MUST refuse to dispatch a task whose
// declared lane is missing or malformed in policy").
func LoadPolicy(path string) (*Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read policy: %w", err)
	}
	return ParsePolicy(string(data))
}

// ParsePolicy parses policy.yaml content (line-based YAML subset, same approach
// as verify.ParseProjectConfig — no third-party YAML library).
func ParsePolicy(data string) (*Policy, error) {
	p := &Policy{Lanes: make(map[string]Lane)}
	var currentLane string
	inLanes := false

	for _, line := range strings.Split(data, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if trimmed == "lanes:" {
			inLanes = true
			continue
		}

		// Lane definitions (indented under "lanes:")
		if inLanes {
			indent := len(line) - len(strings.TrimLeft(line, " "))
			// A non-indented key means we've left the lanes section
			if indent == 0 && strings.Contains(trimmed, ":") {
				inLanes = false
				// Fall through to top-level key handling below
			} else {
				if indent == 2 && strings.HasSuffix(trimmed, ":") {
					currentLane = strings.TrimSuffix(trimmed, ":")
					p.Lanes[currentLane] = Lane{Name: currentLane}
					continue
				}
				if indent >= 4 && currentLane != "" {
					key, val := splitKV(trimmed)
					lane := p.Lanes[currentLane]
					switch key {
					case "max_usd":
						fmt.Sscanf(val, "%f", &lane.MaxUSD)
					case "max_minutes":
						fmt.Sscanf(val, "%d", &lane.MaxMinutes)
					case "review":
						lane.Review = val
					case "test_first":
						lane.TestFirst = val == "true"
					case "deliberation":
						lane.Deliberation = val == "true"
					}
					p.Lanes[currentLane] = lane
				}
				continue
			}
		}

		// Top-level keys outside lanes
		if strings.Contains(trimmed, ":") {
			key, val := splitKV(trimmed)
			switch key {
			case "api_spend_without_approval_usd_per_day":
				var f float64
				if _, err := fmt.Sscanf(val, "%f", &f); err != nil {
					return nil, fmt.Errorf("invalid api_spend_without_approval_usd_per_day: %s", val)
				}
				p.APISpendWithoutApprovalPerDay = f
			case "claude_subscription_unattended":
				p.ClaudeSubscriptionUnattended = val == "true"
			}
			continue
		}

		// Lane definitions (indented under "lanes:")
		if inLanes {
			indent := len(line) - len(strings.TrimLeft(line, " "))
			if indent == 2 && strings.HasSuffix(trimmed, ":") {
				currentLane = strings.TrimSuffix(trimmed, ":")
				p.Lanes[currentLane] = Lane{Name: currentLane}
				continue
			}
			if indent >= 4 && currentLane != "" {
				key, val := splitKV(trimmed)
				lane := p.Lanes[currentLane]
				switch key {
				case "max_usd":
					fmt.Sscanf(val, "%f", &lane.MaxUSD)
				case "max_minutes":
					fmt.Sscanf(val, "%d", &lane.MaxMinutes)
				case "review":
					lane.Review = val
				case "test_first":
					lane.TestFirst = val == "true"
				case "deliberation":
					lane.Deliberation = val == "true"
				}
				p.Lanes[currentLane] = lane
			}
		}
	}

	// Validate: all three lanes must be present and well-formed (FR-009).
	for _, name := range []string{"F", "S", "D"} {
		lane, ok := p.Lanes[name]
		if !ok {
			return nil, fmt.Errorf("lane %s missing from policy", name)
		}
		if lane.MaxUSD <= 0 {
			return nil, fmt.Errorf("lane %s: max_usd must be positive", name)
		}
		if lane.MaxMinutes <= 0 {
			return nil, fmt.Errorf("lane %s: max_minutes must be positive", name)
		}
		if lane.Review != "light" && lane.Review != "full" {
			return nil, fmt.Errorf("lane %s: review must be 'light' or 'full', got %q", name, lane.Review)
		}
	}

	return p, nil
}

// CheckBudget returns an error if adding estimatedCost to accumulatedUSD would
// exceed the lane's max_usd (FR-010: "block dispatch before an overspend occurs").
func CheckBudget(lane Lane, accumulatedUSD, estimatedCost float64) error {
	if accumulatedUSD+estimatedCost > lane.MaxUSD {
		return fmt.Errorf("budget exceeded: %s lane cap $%.2f, accumulated $%.2f, estimated $%.2f",
			lane.Name, lane.MaxUSD, accumulatedUSD, estimatedCost)
	}
	return nil
}

func splitKV(s string) (string, string) {
	idx := strings.Index(s, ":")
	if idx < 0 {
		return strings.TrimSpace(s), ""
	}
	return strings.TrimSpace(s[:idx]), strings.TrimSpace(s[idx+1:])
}
