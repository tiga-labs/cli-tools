package urlutil

import "testing"

func TestHostFromWebsite(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"acme.com", "acme.com"},
		{"www.acme.com", "acme.com"},
		{"awww.com", "awww.com"},
		{"mywww.site.io", "mywww.site.io"},
		{"https://www.acme.com:8080/x", "acme.com"},
		{"WWW.Acme.COM", "acme.com"},
		{"HTTPS://Acme.com/path?q=1", "acme.com"},
		{"https:///acme.com", "acme.com"},
		{"http://sub.www.acme.com", "sub.www.acme.com"},
		{"  acme.com  ", "acme.com"},
	}
	for _, tt := range tests {
		got, err := HostFromWebsite(tt.in)
		if err != nil {
			t.Errorf("HostFromWebsite(%q) error: %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("HostFromWebsite(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestGetDomainFromWebsite(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"acme.com", "acme.com"},
		{"https://app.eu.acme.com/login", "acme.com"},
		{"www.acme.co.uk", "acme.co.uk"},
		{"localhost", ""},
	}
	for _, tt := range tests {
		if got := GetDomainFromWebsite(tt.in); got != tt.want {
			t.Errorf("GetDomainFromWebsite(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestLinkedinUrlToLinkedinId(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"https://www.linkedin.com/in/janedoe", "janedoe"},
		{"https://linkedin.com/in/JaneDoe/", "janedoe"},
		{"https://www.linkedin.com/in/janedoe?trk=abc", "janedoe"},
		{"https://www.linkedin.com/company/acme", ""},
	}
	for _, tt := range tests {
		if got := LinkedinUrlToLinkedinId(tt.in); got != tt.want {
			t.Errorf("LinkedinUrlToLinkedinId(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCleanPersonLinkedinUrl(t *testing.T) {
	got, err := CleanPersonLinkedinUrl(" HTTP://LinkedIn.com/in/JaneDoe/?trk=x ")
	if err != nil || got != "https://www.linkedin.com/in/janedoe" {
		t.Errorf("CleanPersonLinkedinUrl = %q, %v", got, err)
	}
	if _, err := CleanPersonLinkedinUrl("https://www.linkedin.com/company/acme"); err == nil {
		t.Errorf("expected error for company URL")
	}
}

func TestCleanCompanyLinkedinUrl(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"https://www.linkedin.com/company/Acme/about/", "https://www.linkedin.com/company/acme"},
		{"linkedin.com/showcase/acme-cloud?x=1", "https://www.linkedin.com/showcase/acme-cloud"},
		{"https://www.linkedin.com/school/stanford-university/", "https://www.linkedin.com/school/stanford-university"},
	}
	for _, tt := range tests {
		got, err := CleanCompanyLinkedinUrl(tt.in)
		if err != nil || got != tt.want {
			t.Errorf("CleanCompanyLinkedinUrl(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
	if _, err := CleanCompanyLinkedinUrl("https://www.linkedin.com/in/janedoe"); err == nil {
		t.Errorf("expected error for person URL")
	}
}
