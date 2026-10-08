package verify

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ProjectConfig defines the verification and diff policy settings in .foreman/project.yaml.
type ProjectConfig struct {
	Version        int        `json:"version"`
	VerifierImage  string     `json:"verifier_image"`
	Setup          [][]string `json:"setup"`
	Verify         [][]string `json:"verify"`
	ProtectedPaths []string   `json:"protected_paths"`
}

var ErrInvalidConfig = errors.New("invalid project config")

// ParseProjectConfig parses .foreman/project.yaml using standard library.
// It supports JSON or line-based subset YAML without external dependencies.
func ParseProjectConfig(data []byte) (ProjectConfig, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var cfg ProjectConfig
		if err := json.Unmarshal(trimmed, &cfg); err != nil {
			return ProjectConfig{}, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
		}
		if err := validateProjectConfig(cfg); err != nil {
			return ProjectConfig{}, err
		}
		return cfg, nil
	}

	var cfg ProjectConfig
	scanner := bufio.NewScanner(bytes.NewReader(data))
	var currentSection string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if strings.HasPrefix(line, "version:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "version:"))
			v, err := strconv.Atoi(val)
			if err != nil {
				return ProjectConfig{}, fmt.Errorf("%w: version must be an integer: %v", ErrInvalidConfig, err)
			}
			cfg.Version = v
			currentSection = ""
			continue
		}

		if strings.HasPrefix(line, "verifier_image:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "verifier_image:"))
			val = strings.Trim(val, "\x22'")
			cfg.VerifierImage = val
			currentSection = ""
			continue
		}

		if strings.HasPrefix(line, "setup:") {
			currentSection = "setup"
			continue
		}

		if strings.HasPrefix(line, "verify:") {
			currentSection = "verify"
			continue
		}

		if strings.HasPrefix(line, "protected_paths:") {
			currentSection = "protected_paths"
			continue
		}

		if strings.HasPrefix(line, "-") {
			item := strings.TrimSpace(strings.TrimPrefix(line, "-"))
			switch currentSection {
			case "setup":
				cmd, err := parseStringList(item)
				if err != nil {
					return ProjectConfig{}, fmt.Errorf("%w: invalid setup command: %v", ErrInvalidConfig, err)
				}
				cfg.Setup = append(cfg.Setup, cmd)
			case "verify":
				cmd, err := parseStringList(item)
				if err != nil {
					return ProjectConfig{}, fmt.Errorf("%w: invalid verify command: %v", ErrInvalidConfig, err)
				}
				cfg.Verify = append(cfg.Verify, cmd)
			case "protected_paths":
				path := strings.Trim(item, "\x22'")
				cfg.ProtectedPaths = append(cfg.ProtectedPaths, path)
			default:
				return ProjectConfig{}, fmt.Errorf("%w: item without section: %s", ErrInvalidConfig, line)
			}
			continue
		}

		return ProjectConfig{}, fmt.Errorf("%w: unexpected line: %s", ErrInvalidConfig, line)
	}

	if err := scanner.Err(); err != nil {
		return ProjectConfig{}, err
	}
	if err := validateProjectConfig(cfg); err != nil {
		return ProjectConfig{}, err
	}
	return cfg, nil
}

func validateProjectConfig(cfg ProjectConfig) error {
	if cfg.Version != 1 {
		return fmt.Errorf("%w: version must be 1", ErrInvalidConfig)
	}
	if strings.TrimSpace(cfg.VerifierImage) == "" {
		return fmt.Errorf("%w: verifier_image is required", ErrInvalidConfig)
	}
	if len(cfg.Verify) == 0 {
		return fmt.Errorf("%w: at least one verify command is required", ErrInvalidConfig)
	}
	for i, cmd := range append(append([][]string(nil), cfg.Setup...), cfg.Verify...) {
		if len(cmd) == 0 || strings.TrimSpace(cmd[0]) == "" {
			return fmt.Errorf("%w: command %d cannot be empty", ErrInvalidConfig, i)
		}
	}
	return nil
}

func parseStringList(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		var list []string
		if err := json.Unmarshal([]byte(s), &list); err != nil {
			return nil, err
		}
		return list, nil
	}
	parts := strings.Fields(s)
	return parts, nil
}
