// Package keystore persists vendor API keys in the config file shared by every Tiga CLI tool.
package keystore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Vendor describes one vendor a tool knows about: its canonical name, a display category,
// and the environment variables that override the stored key (checked in order).
type Vendor struct {
	Name     string
	Category string
	EnvVars  []string
}

// Config holds the persisted vendor API keys. The file is shared across tools, so Keys may
// contain vendors the current tool's registry does not list; they are kept on Save.
// Construct it with Load or New: a zero-value Config has no vendor registry, so it ignores
// environment overrides and rejects every vendor name.
type Config struct {
	Keys    map[string]string `json:"keys"`
	vendors []Vendor
}

// New returns an in-memory Config for a vendor registry (used when no file is involved).
func New(vendors []Vendor, keys map[string]string) *Config {
	if keys == nil {
		keys = make(map[string]string)
	}
	return &Config{Keys: keys, vendors: vendors}
}

// DefaultPath returns ~/.config/tiga/config.json (or $XDG_CONFIG_HOME/tiga/config.json).
func DefaultPath() (string, error) {
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "tiga", "config.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "tiga", "config.json"), nil
}

// Load loads the config file at path (DefaultPath when empty) for a tool with the given vendor
// registry. If the file does not exist, an empty Config is returned.
func Load(path string, vendors []Vendor) (*Config, error) {
	if strings.TrimSpace(path) == "" {
		var err error
		path, err = DefaultPath()
		if err != nil {
			return nil, err
		}
	}

	cfg := &Config{Keys: make(map[string]string), vendors: vendors}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return nil, err
	}

	if len(strings.TrimSpace(string(data))) == 0 {
		return cfg, nil
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file %s: %w", path, err)
	}
	if cfg.Keys == nil {
		cfg.Keys = make(map[string]string)
	}
	return cfg, nil
}

// Save writes the Config to path (DefaultPath when empty) with 0600 permissions, creating
// parent directories if needed.
func Save(path string, cfg *Config) error {
	if strings.TrimSpace(path) == "" {
		var err error
		path, err = DefaultPath()
		if err != nil {
			return err
		}
	}
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.Keys == nil {
		cfg.Keys = make(map[string]string)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	// Write the real file behind a symlinked config (e.g. dotfiles) rather than replacing the link.
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return writeFileAtomic(path, data, 0600)
}

// writeFileAtomic writes data to a temp file in path's directory, enforces perm, and renames it
// over path, so a crash never leaves a truncated file and an existing file's mode is corrected.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename

	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (c *Config) vendorNames() []string {
	if c == nil {
		return nil
	}
	names := make([]string, 0, len(c.vendors))
	for _, v := range c.vendors {
		names = append(names, v.Name)
	}
	return names
}

func (c *Config) vendor(name string) (Vendor, bool) {
	if c == nil {
		return Vendor{}, false
	}
	for _, v := range c.vendors {
		if v.Name == name {
			return v, true
		}
	}
	return Vendor{}, false
}

// NormalizeVendor normalizes a vendor name (case, '_' and '-' ignored) and validates it
// against the tool's registry.
func (c *Config) NormalizeVendor(name string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(name))
	v = strings.ReplaceAll(v, "_", "")
	v = strings.ReplaceAll(v, "-", "")
	if _, ok := c.vendor(v); ok {
		return v, nil
	}
	return "", fmt.Errorf("unsupported vendor %q (supported: %s)", name, strings.Join(c.vendorNames(), ", "))
}

// SetKey sets a vendor API key in the config. Call Save to persist it.
func (c *Config) SetKey(vendor, apiKey string) error {
	norm, err := c.NormalizeVendor(vendor)
	if err != nil {
		return err
	}
	trimmed := strings.TrimSpace(apiKey)
	if trimmed == "" {
		return errors.New("api key cannot be empty")
	}
	if c.Keys == nil {
		c.Keys = make(map[string]string)
	}
	c.Keys[norm] = trimmed
	return nil
}

// RemoveKey removes a vendor API key from the config. Call Save to persist it.
func (c *Config) RemoveKey(vendor string) error {
	norm, err := c.NormalizeVendor(vendor)
	if err != nil {
		return err
	}
	delete(c.Keys, norm)
	return nil
}

// envKey returns the first non-empty environment override for a vendor and its variable name.
func envKey(v Vendor) (string, string) {
	for _, envName := range v.EnvVars {
		if val := strings.TrimSpace(os.Getenv(envName)); val != "" {
			return val, envName
		}
	}
	return "", ""
}

// GetKey resolves the active API key for a vendor (Environment variable > Config file).
func (c *Config) GetKey(vendor string) string {
	norm, err := c.NormalizeVendor(vendor)
	if err != nil {
		norm = strings.ToLower(strings.TrimSpace(vendor))
	}
	if v, ok := c.vendor(norm); ok {
		if val, _ := envKey(v); val != "" {
			return val
		}
	}
	if c != nil && c.Keys != nil {
		return strings.TrimSpace(c.Keys[norm])
	}
	return ""
}

// RedactSecrets replaces every key stored in the file (including other tools' vendors) and
// every active key of this tool's vendors found in s with "[REDACTED]".
func (c *Config) RedactSecrets(s string) string {
	if c == nil {
		return s
	}
	seen := make(map[string]bool)
	var keys []string
	add := func(key string) {
		if key = strings.TrimSpace(key); key != "" && !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	for _, key := range c.Keys {
		add(key)
	}
	for _, v := range c.vendors {
		add(c.GetKey(v.Name))
	}
	// Longest first, so a key that contains another key is never left partly visible.
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for _, key := range keys {
		s = strings.ReplaceAll(s, key, "[REDACTED]")
	}
	return s
}

// KeyStatus describes the configuration state of a single vendor key.
type KeyStatus struct {
	Vendor     string `json:"vendor"`
	Category   string `json:"category"`
	Configured bool   `json:"configured"`
	Source     string `json:"source"` // "env (<VAR>)", "config", or "none"
	MaskedKey  string `json:"masked_key"`
}

// ListKeyStatuses returns the status of every vendor in the tool's registry, in registry order.
func (c *Config) ListKeyStatuses() []KeyStatus {
	if c == nil {
		return nil
	}
	statuses := make([]KeyStatus, 0, len(c.vendors))
	for _, v := range c.vendors {
		rawKey, envName := envKey(v)
		source := "none"
		if rawKey != "" {
			source = "env (" + envName + ")"
		} else if c.Keys != nil {
			if val := strings.TrimSpace(c.Keys[v.Name]); val != "" {
				rawKey = val
				source = "config"
			}
		}
		statuses = append(statuses, KeyStatus{
			Vendor:     v.Name,
			Category:   v.Category,
			Configured: rawKey != "",
			Source:     source,
			MaskedKey:  MaskKey(rawKey),
		})
	}
	return statuses
}

// MaskKey masks an API key for display. Short keys reveal less so that the masked form never
// exposes most of the key: <12 chars → "****", <24 → "...wxyz", otherwise "abcd...wxyz".
func MaskKey(k string) string {
	k = strings.TrimSpace(k)
	switch {
	case k == "":
		return ""
	case len(k) < 12:
		return "****"
	case len(k) < 24:
		return "..." + k[len(k)-4:]
	default:
		return k[:4] + "..." + k[len(k)-4:]
	}
}
