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
	Name      string
	Values    map[string]string
	ConfigDir string // directory of the config file, for key file fallback resolution
}

// ParseFile reads an OCI config file and returns all profiles.
// The DEFAULT section is applied as a fallback to every named profile.
func ParseFile(path string) ([]Profile, error) {
	// Expand ~ in path
	path = expandHome(path)

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
		// Resolve key_file: expand ~ then resolve relative paths against configDir.
		if kf, ok := merged["key_file"]; ok && kf != "" {
			merged["key_file"] = resolveKeyFilePath(kf, configDir)
		}
		result = append(result, Profile{Name: p.Name, Values: merged, ConfigDir: configDir})
	}

	// If only DEFAULT was defined (no named sections), treat DEFAULT itself as one profile.
	if len(result) == 0 && len(defaults) > 0 {
		merged := map[string]string{}
		for k, v := range defaults {
			merged[k] = v
		}
		if kf, ok := merged["key_file"]; ok && kf != "" {
			merged["key_file"] = resolveKeyFilePath(kf, configDir)
		}
		result = append(result, Profile{Name: "DEFAULT", Values: merged, ConfigDir: configDir})
	}

	return result, nil
}

// ParseContent parses an OCI config from raw text (for browser upload).
// key_file paths are kept as-is; caller must resolve them separately.
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
	KeyFilePath  string // resolved path (or raw path for upload)
	PrivateKey   string // resolved PEM content (empty if not yet read)
	KeyFileError string // non-empty if key_file could not be read
}

// ToEntries converts raw profiles into ProfileEntry structs, reading key files when possible.
// When readKeyFiles is true, it tries to read the PEM key at the resolved path,
// with a fallback to basename(key_file) inside the profile's ConfigDir.
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
			pem, errMsg := readKeyFileWithFallback(e.KeyFilePath, p.ConfigDir)
			if errMsg != "" {
				e.KeyFileError = errMsg
			} else {
				e.PrivateKey = pem
			}
		}
		entries = append(entries, e)
	}
	return entries
}

// readKeyFileWithFallback tries to read the key file from:
//  1. The path as-is (already resolved absolute or relative to configDir)
//  2. Basename of the path relative to configDir (handles absolute paths from another machine)
func readKeyFileWithFallback(keyFilePath, configDir string) (string, string) {
	if data, err := os.ReadFile(keyFilePath); err == nil {
		return string(data), ""
	}

	// Fallback: try just the filename in the config directory
	if configDir != "" {
		alt := filepath.Join(configDir, filepath.Base(keyFilePath))
		if alt != keyFilePath {
			if data, err := os.ReadFile(alt); err == nil {
				return string(data), ""
			}
		}
	}

	if configDir != "" {
		alt := filepath.Join(configDir, filepath.Base(keyFilePath))
		return "", fmt.Sprintf("key file not found: tried %q and %q", keyFilePath, alt)
	}
	return "", fmt.Sprintf("key file not found: %q", keyFilePath)
}

// resolveKeyFilePath expands ~ and resolves relative paths against configDir.
func resolveKeyFilePath(keyFile, configDir string) string {
	keyFile = expandHome(keyFile)
	if !filepath.IsAbs(keyFile) {
		keyFile = filepath.Join(configDir, keyFile)
	}
	return keyFile
}

// expandHome replaces a leading ~ with the user's home directory.
func expandHome(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}
