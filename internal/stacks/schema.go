package stacks

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// StackConfig is the parsed form of a stack YAML file.
type StackConfig struct {
	APIVersion string            `yaml:"apiVersion"`
	Kind       string            `yaml:"kind"`
	Metadata   StackMetadata     `yaml:"metadata"`
	Config     map[string]string `yaml:"config"`
}

type StackMetadata struct {
	Name        string `yaml:"name"`
	Program     string `yaml:"program"`
	Description string `yaml:"description,omitempty"`
}

// Validate checks the config is structurally valid.
func (s *StackConfig) Validate() error {
	if s.Metadata.Name == "" {
		return fmt.Errorf("metadata.name is required")
	}
	if s.Metadata.Program == "" {
		return fmt.Errorf("metadata.program is required")
	}
	if s.APIVersion != "pulumi.io/v1" {
		return fmt.Errorf("unsupported apiVersion: %s", s.APIVersion)
	}
	if s.Kind != "Stack" {
		return fmt.Errorf("kind must be Stack, got: %s", s.Kind)
	}
	return nil
}

// ToYAML serialises the config back to a YAML string.
func (s *StackConfig) ToYAML() (string, error) {
	data, err := yaml.Marshal(s)
	return string(data), err
}

// ParseYAML parses a YAML string into a StackConfig.
func ParseYAML(data string) (*StackConfig, error) {
	var cfg StackConfig
	if err := yaml.Unmarshal([]byte(data), &cfg); err != nil {
		return nil, fmt.Errorf("invalid YAML: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// LoadFromFile reads a YAML file from disk.
func LoadFromFile(path string) (*StackConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseYAML(string(data))
}

// SaveToFile writes a StackConfig to disk as YAML.
func SaveToFile(cfg *StackConfig, dir string) error {
	data, err := cfg.ToYAML()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, cfg.Metadata.Name+".yaml")
	return os.WriteFile(path, []byte(data), 0644)
}
