package enrich

import (
	"slices"
	"testing"

	"github.com/tiga-labs/cli-tools/pkg/salesnav"
)

// unmapped lists the salesnav-list-import columns enrich does not pick up on its own.
func unmapped(t *testing.T, header []string) []string {
	t.Helper()
	idx, err := resolveColumnIndices(header, ColumnOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{
		"first_name": idx.firstName, "last_name": idx.lastName, "full_name": idx.fullName,
		"title": idx.title, "company_name": idx.companyName, "company_linkedin": idx.companyLinkedIn,
		"linkedin_urn": idx.linkedInURN,
	}
	var missing []string
	for col, i := range want {
		if i < 0 || header[i] != col {
			missing = append(missing, col)
		}
	}
	slices.Sort(missing)
	return missing
}

// `tiga enrich csv` must read a salesnav-list-import CSV with no column flags.
func TestSalesNavHeaderAutoMaps(t *testing.T) {
	if m := unmapped(t, salesnav.CSVHeader); len(m) > 0 {
		t.Errorf("enrich does not auto-map %v", m)
	}
	// The check itself can fail: a renamed URN column is reported.
	renamed := slices.Clone(salesnav.CSVHeader)
	renamed[slices.Index(renamed, "linkedin_urn")] = "xx"
	if m := unmapped(t, renamed); !slices.Equal(m, []string{"linkedin_urn"}) {
		t.Errorf("renamed header: unmapped = %v, want [linkedin_urn]", m)
	}
}
