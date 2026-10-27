package enrich

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config holds the user's persisted vendor API keys.
type Config struct {
	Keys map[string]string `json:"keys"`
}

// vendorEnvAliases maps canonical vendor names to supported environment variables
// (checking both standard <VENDOR>_API_KEY and legacy Gandalf env var names).
var vendorEnvAliases = map[string][]string{
	VendorFindymail:   {"FINDYMAIL_API_KEY", "FINDY_MAIL", "FINDYMAIL_KEY"},
	VendorProspeo:     {"PROSPEO_API_KEY", "PROSPEO_KEY"},
	VendorApollo:      {"APOLLO_API_KEY", "APOLLO_KEY"},
	VendorLusha:       {"LUSHA_API_KEY", "LUSHA_KEY"},
	VendorSeamless:    {"SEAMLESS_API_KEY", "SEAMLESS_KEY"},
	VendorDropcontact: {"DROPCONTACT_API_KEY", "DROPCONTACT_KEY"},
	VendorHunter:      {"HUNTER_API_KEY", "HUNTER_KEY"},
	VendorDatagma:     {"DATAGMA_API_KEY", "DATAGMA_KEY"},
	VendorLeadIQ:      {"LEADIQ_API_KEY", "LEAD_IQ_KEY", "LEADIQ_KEY"},
	VendorRenidly:     {"RENIDLY_API_KEY", "RENIDLY_KEY"},
	VendorZeroBounce:  {"ZEROBOUNCE_API_KEY", "ZERO_BOUNCE", "ZEROBOUNCE_KEY"},
	VendorOpenAI:      {"OPENAI_API_KEY", "OPENAI_KEY"},
}

// NormalizeVendorName normalizes and validates a vendor name against AllSupportedVendors.
func NormalizeVendorName(name string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(name))
	v = strings.ReplaceAll(v, "_", "")
	v = strings.ReplaceAll(v, "-", "")
	for _, supported := range AllSupportedVendors {
		if v == supported {
			return supported, nil
		}
	}
	return "", fmt.Errorf("unsupported vendor %q (supported: %s)", name, strings.Join(AllSupportedVendors, ", "))
}

// IsWaterfallVendor reports whether a vendor is one of the 9 contact waterfall vendors.
func IsWaterfallVendor(vendor string) bool {
	for _, w := range DefaultWaterfallOrder {
		if w == vendor {
			return true
		}
	}
	return false
}

// DefaultConfigPath returns ~/.config/enrich/config.json (or $XDG_CONFIG_HOME/enrich/config.json).
func DefaultConfigPath() (string, error) {
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "enrich", "config.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "enrich", "config.json"), nil
}

// LoadConfig loads the config file at path. If the file does not exist, an empty Config is returned.
func LoadConfig(path string) (*Config, error) {
	if strings.TrimSpace(path) == "" {
		var err error
		path, err = DefaultConfigPath()
		if err != nil {
			return nil, err
		}
	}

	cfg := &Config{Keys: make(map[string]string)}
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

// SaveConfig writes the Config to path with 0600 permissions, creating parent directories if needed.
func SaveConfig(path string, cfg *Config) error {
	if strings.TrimSpace(path) == "" {
		var err error
		path, err = DefaultConfigPath()
		if err != nil {
			return err
		}
	}
	if cfg == nil {
		cfg = &Config{Keys: make(map[string]string)}
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

// SetKey sets a vendor API key in the config and saves it.
func (c *Config) SetKey(vendor, apiKey string) error {
	norm, err := NormalizeVendorName(vendor)
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

// RemoveKey removes a vendor API key from the config.
func (c *Config) RemoveKey(vendor string) error {
	norm, err := NormalizeVendorName(vendor)
	if err != nil {
		return err
	}
	delete(c.Keys, norm)
	return nil
}

// GetKey resolves the active API key for a vendor (Environment variable > Config file).
func (c *Config) GetKey(vendor string) string {
	norm, err := NormalizeVendorName(vendor)
	if err != nil {
		norm = strings.ToLower(strings.TrimSpace(vendor))
	}
	for _, envName := range vendorEnvAliases[norm] {
		if val := strings.TrimSpace(os.Getenv(envName)); val != "" {
			return val
		}
	}
	if c != nil && c.Keys != nil {
		return strings.TrimSpace(c.Keys[norm])
	}
	return ""
}

// RedactSecrets replaces every configured vendor API key found in s with "[REDACTED]".
func (c *Config) RedactSecrets(s string) string {
	for _, v := range AllSupportedVendors {
		if key := c.GetKey(v); key != "" {
			s = strings.ReplaceAll(s, key, "[REDACTED]")
		}
	}
	return s
}

// KeyStatus describes the configuration state of a single vendor key.
type KeyStatus struct {
	Vendor     string `json:"vendor"`
	Category   string `json:"category"` // "waterfall" or "feature"
	Configured bool   `json:"configured"`
	Source     string `json:"source"` // "env", "config", or "none"
	MaskedKey  string `json:"masked_key"`
}

// ListKeyStatuses returns the status for all 12 supported vendors.
func (c *Config) ListKeyStatuses() []KeyStatus {
	statuses := make([]KeyStatus, 0, len(AllSupportedVendors))
	for _, v := range AllSupportedVendors {
		cat := "feature"
		if IsWaterfallVendor(v) {
			cat = "waterfall"
		}
		var rawKey string
		source := "none"

		for _, envName := range vendorEnvAliases[v] {
			if val := strings.TrimSpace(os.Getenv(envName)); val != "" {
				rawKey = val
				source = "env (" + envName + ")"
				break
			}
		}
		if rawKey == "" && c != nil && c.Keys != nil {
			if val := strings.TrimSpace(c.Keys[v]); val != "" {
				rawKey = val
				source = "config"
			}
		}

		statuses = append(statuses, KeyStatus{
			Vendor:     v,
			Category:   cat,
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

// ResolveActiveWaterfallVendors returns the ordered list of waterfall vendors to run,
// filtering out excluded vendors and ensuring each vendor has a configured API key.
func (c *Config) ResolveActiveWaterfallVendors(requested []string, excluded []string) ([]string, error) {
	excludeMap := make(map[string]bool, len(excluded))
	for _, ex := range excluded {
		if strings.TrimSpace(ex) == "" {
			continue
		}
		norm, err := NormalizeVendorName(ex)
		if err != nil {
			return nil, err
		}
		excludeMap[norm] = true
	}

	var candidates []string
	if len(requested) > 0 {
		seen := make(map[string]bool)
		for _, req := range requested {
			if strings.TrimSpace(req) == "" {
				continue
			}
			norm, err := NormalizeVendorName(req)
			if err != nil {
				return nil, err
			}
			if !IsWaterfallVendor(norm) {
				return nil, fmt.Errorf("%q is a feature provider, not a waterfall contact provider", norm)
			}
			if !seen[norm] {
				seen[norm] = true
				candidates = append(candidates, norm)
			}
		}
	} else {
		candidates = append(candidates, DefaultWaterfallOrder...)
	}

	var active []string
	for _, v := range candidates {
		if excludeMap[v] {
			continue
		}
		if c.GetKey(v) != "" {
			active = append(active, v)
		} else if len(requested) > 0 {
			// If the user explicitly requested a vendor that has no key, return a clear error.
			return nil, fmt.Errorf("vendor %q was explicitly requested but has no API key configured", v)
		}
	}

	return active, nil
}
