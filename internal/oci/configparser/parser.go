// Package configparser parses OCI SDK config files (INI format with DEFAULT inheritance).
package configparser

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Profile holds the raw key/value pairs from one INI section.
type Profile struct {
	Name   string
	Values map[string]string
}

// ParseFile reads an OCI config file and returns all profiles.
// The DEFAULT section is applied as a fallback to every named profile.
// configDir is used to resolve relative key_file paths.
func ParseFile(path string) ([]Profile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config file: %w", err)
	}
	defer f.Close()

	configDir := filepath.Dir(path)

	var profiles []Profile
	defaults := map[string]string{}
	var current *Profile

	flush := func() {
		if current != nil {
			profiles = append(profiles, *current)
			current = nil
		}
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			flush()
			name := line[1 : len(line)-1]
			if name == "DEFAULT" {
				current = &Profile{Name: "DEFAULT", Values: defaults}
			} else {
				current = &Profile{Name: name, Values: map[string]string{}}
			}
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		if current != nil {
			current.Values[key] = val
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	flush()

	// Apply DEFAULT values as fallback for each named profile and resolve key_file.
	result := make([]Profile, 0, len(profiles))
	for _, p := range profiles {
		if p.Name == "DEFAULT" {
			continue
		}
		merged := map[string]string{}
		for k, v := range defaults {
			merged[k] = v
		}
		for k, v := range p.Values {
			merged[k] = v
		}
		// Resolve key_file relative to config file location.
		if kf, ok := merged["key_file"]; ok && kf != "" && !filepath.IsAbs(kf) {
			merged["key_file"] = filepath.Join(configDir, kf)
		}
		result = append(result, Profile{Name: p.Name, Values: merged})
	}

	// If only DEFAULT was defined (no named sections), treat DEFAULT itself as one profile.
	if len(result) == 0 && len(defaults) > 0 {
		merged := map[string]string{}
		for k, v := range defaults {
			merged[k] = v
		}
		if kf, ok := merged["key_file"]; ok && kf != "" && !filepath.IsAbs(kf) {
			merged["key_file"] = filepath.Join(configDir, kf)
		}
		result = append(result, Profile{Name: "DEFAULT", Values: merged})
	}

	return result, nil
}

// ParseContent parses an OCI config from raw text (for browser upload).
// key_file paths are kept as-is (caller must handle resolution separately).
func ParseContent(content string) ([]Profile, error) {
	var profiles []Profile
	defaults := map[string]string{}
	var current *Profile

	flush := func() {
		if current != nil {
			profiles = append(profiles, *current)
			current = nil
		}
	}

	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			flush()
			name := line[1 : len(line)-1]
			if name == "DEFAULT" {
				current = &Profile{Name: "DEFAULT", Values: defaults}
			} else {
				current = &Profile{Name: name, Values: map[string]string{}}
			}
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		if current != nil {
			current.Values[key] = val
		}
	}
	flush()

	result := make([]Profile, 0, len(profiles))
	for _, p := range profiles {
		if p.Name == "DEFAULT" {
			continue
		}
		merged := map[string]string{}
		for k, v := range defaults {
			merged[k] = v
		}
		for k, v := range p.Values {
			merged[k] = v
		}
		result = append(result, Profile{Name: p.Name, Values: merged})
	}
	if len(result) == 0 && len(defaults) > 0 {
		merged := map[string]string{}
		for k, v := range defaults {
			merged[k] = v
		}
		result = append(result, Profile{Name: "DEFAULT", Values: merged})
	}
	return result, nil
}

// ProfileEntry is a parsed, validated profile ready for account creation.
type ProfileEntry struct {
	ProfileName  string
	TenancyOCID  string
	UserOCID     string
	Fingerprint  string
	Region       string
	KeyFilePath  string // empty for upload-based imports
	PrivateKey   string // resolved PEM content (empty if not yet read)
	KeyFileError string // non-empty if key_file could not be read
}

// ToEntries converts raw profiles into ProfileEntry structs, reading key files when possible.
func ToEntries(profiles []Profile, readKeyFiles bool) []ProfileEntry {
	entries := make([]ProfileEntry, 0, len(profiles))
	for _, p := range profiles {
		e := ProfileEntry{
			ProfileName: p.Name,
			TenancyOCID: p.Values["tenancy"],
			UserOCID:    p.Values["user"],
			Fingerprint: p.Values["fingerprint"],
			Region:      p.Values["region"],
			KeyFilePath: p.Values["key_file"],
		}
		if readKeyFiles && e.KeyFilePath != "" {
			pem, err := os.ReadFile(e.KeyFilePath)
			if err != nil {
				e.KeyFileError = fmt.Sprintf("cannot read key file: %s", err.Error())
			} else {
				e.PrivateKey = string(pem)
			}
		}
		entries = append(entries, e)
	}
	return entries
}
