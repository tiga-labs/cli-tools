package enrich

import (
	"fmt"
	"strings"

	"github.com/tiga-labs/cli-tools/pkg/keystore"
)

// Config is the shared Tiga key store, loaded with enrich's vendor registry.
type Config = keystore.Config

// Vendors is enrich's vendor registry: canonical name, category, and the supported environment
// variables (standard <VENDOR>_API_KEY plus legacy Gandalf names), in AllSupportedVendors order.
var Vendors = []keystore.Vendor{
	{Name: VendorFindymail, Category: "waterfall", EnvVars: []string{"FINDYMAIL_API_KEY", "FINDY_MAIL", "FINDYMAIL_KEY"}},
	{Name: VendorProspeo, Category: "waterfall", EnvVars: []string{"PROSPEO_API_KEY", "PROSPEO_KEY"}},
	{Name: VendorApollo, Category: "waterfall", EnvVars: []string{"APOLLO_API_KEY", "APOLLO_KEY"}},
	{Name: VendorLusha, Category: "waterfall", EnvVars: []string{"LUSHA_API_KEY", "LUSHA_KEY"}},
	{Name: VendorSeamless, Category: "waterfall", EnvVars: []string{"SEAMLESS_API_KEY", "SEAMLESS_KEY"}},
	{Name: VendorDropcontact, Category: "waterfall", EnvVars: []string{"DROPCONTACT_API_KEY", "DROPCONTACT_KEY"}},
	{Name: VendorHunter, Category: "waterfall", EnvVars: []string{"HUNTER_API_KEY", "HUNTER_KEY"}},
	{Name: VendorDatagma, Category: "waterfall", EnvVars: []string{"DATAGMA_API_KEY", "DATAGMA_KEY"}},
	{Name: VendorLeadIQ, Category: "waterfall", EnvVars: []string{"LEADIQ_API_KEY", "LEAD_IQ_KEY", "LEADIQ_KEY"}},
	{Name: VendorRenidly, Category: "feature", EnvVars: []string{"RENIDLY_API_KEY", "RENIDLY_KEY"}},
	{Name: VendorZeroBounce, Category: "feature", EnvVars: []string{"ZEROBOUNCE_API_KEY", "ZERO_BOUNCE", "ZEROBOUNCE_KEY"}},
	{Name: VendorOpenAI, Category: "feature", EnvVars: []string{"OPENAI_API_KEY", "OPENAI_KEY"}},
}

// LoadConfig loads the shared key file at path (keystore.DefaultPath when empty) with enrich's vendors.
func LoadConfig(path string) (*Config, error) {
	return keystore.Load(path, Vendors)
}

// SaveConfig writes the shared key file; keys of other tools' vendors are preserved.
func SaveConfig(path string, cfg *Config) error {
	return keystore.Save(path, cfg)
}

// NewConfig returns an in-memory Config with enrich's vendors and the given keys.
func NewConfig(keys map[string]string) *Config {
	return keystore.New(Vendors, keys)
}

// NormalizeVendorName normalizes and validates a vendor name against AllSupportedVendors.
func NormalizeVendorName(name string) (string, error) {
	return NewConfig(nil).NormalizeVendor(name)
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

// ResolveActiveWaterfallVendors returns the ordered list of waterfall vendors to run,
// filtering out excluded vendors and ensuring each vendor has a configured API key.
func ResolveActiveWaterfallVendors(c *Config, requested []string, excluded []string) ([]string, error) {
	if c == nil {
		c = NewConfig(nil) // still honor environment keys
	}
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
