package signals

import "github.com/tiga-labs/cli-tools/pkg/keystore"

// Vendor names for the signals CLI's keys.
const (
	VendorOpenAI   = "openai"
	VendorLinkdAPI = "linkdapi"
)

// Vendors is the signals CLI's vendor registry, in display order.
var Vendors = []keystore.Vendor{
	{Name: VendorOpenAI, Category: "research", EnvVars: []string{"OPENAI_API_KEY", "OPENAI_KEY"}},
	{Name: VendorLinkdAPI, Category: "linkedin", EnvVars: []string{"LINKDAPI_API_KEY", "LINKDAPI_KEY"}},
}
