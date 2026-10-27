package urlutil

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// HostFromWebsite extracts the hostname from a website URL or domain string, stripping leading "www.".
func HostFromWebsite(website string) (string, error) {
	website = strings.TrimSpace(website)
	if website == "" {
		return "", nil
	}
	if !strings.Contains(website, "http://") && !strings.Contains(website, "https://") {
		website = "http://" + website
	}
	if strings.Contains(website, "http:///") || strings.Contains(website, "https:///") {
		website = strings.Replace(website, ":///", "://", 1)
	}

	u, err := url.Parse(website)
	if err != nil {
		return "", err
	}

	host := u.Host
	host = strings.Replace(host, "www.", "", 1)
	host = strings.TrimSpace(host)
	return host, nil
}

// GetDomainFromWebsite returns the registrable root domain (eTLD+1) from a website URL or hostname.
func GetDomainFromWebsite(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}

	u, err := url.Parse(s)
	if err != nil {
		return ""
	}

	host := u.Hostname()
	if host == "" {
		return ""
	}

	_, icann := publicsuffix.PublicSuffix(host)
	if icann {
		domain, err := publicsuffix.EffectiveTLDPlusOne(host)
		if err != nil {
			return ""
		}
		return domain
	}

	parts := strings.Split(host, ".")
	if len(parts) < 2 {
		return ""
	}
	return strings.Join(parts[len(parts)-2:], ".")
}

// RemoveQueryParamsFromUrl strips query parameters from a URL string.
func RemoveQueryParamsFromUrl(urlStr string) (string, error) {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return "", err
	}
	parsedURL.RawQuery = ""
	return parsedURL.String(), nil
}

// LinkedinUrlToLinkedinId extracts the public profile handle from a LinkedIn `/in/<handle>` URL.
func LinkedinUrlToLinkedinId(rawURL string) string {
	rawURL = strings.ToLower(rawURL)
	split := strings.Split(rawURL, "linkedin.com/in/")
	if len(split) > 1 {
		id := split[1]
		index := strings.IndexAny(id, "/?.")
		if index == -1 {
			return strings.TrimSpace(id)
		}
		return strings.TrimSpace(id[:index])
	}
	return ""
}

var companyLinkedInRegex = regexp.MustCompile(`linkedin\.com/company/|linkedin\.com/school/|linkedin\.com/showcase/`)

// LinkedinCompanyUrlToLinkedinId extracts the public company/school/showcase slug from a LinkedIn URL.
func LinkedinCompanyUrlToLinkedinId(rawURL string) string {
	rawURL = strings.ToLower(rawURL)
	split := companyLinkedInRegex.Split(rawURL, -1)
	if len(split) > 1 {
		id := split[1]
		if index := strings.IndexAny(id, "/?"); index != -1 {
			id = id[:index]
		}
		return strings.TrimRight(strings.TrimSpace(id), ".")
	}
	return ""
}

// CleanPersonLinkedinUrl canonicalizes a LinkedIn profile URL to `https://www.linkedin.com/in/<id>`.
func CleanPersonLinkedinUrl(linkedinUrlStr string) (string, error) {
	linkedinUrlStr = strings.ToLower(strings.TrimSpace(linkedinUrlStr))
	urlStrParamFree, err := RemoveQueryParamsFromUrl(linkedinUrlStr)
	if err != nil {
		return "", err
	}
	id := LinkedinUrlToLinkedinId(urlStrParamFree)
	if id != "" {
		return "https://www.linkedin.com/in/" + id, nil
	}
	return "", errors.New("invalid linkedin url")
}

// CleanCompanyLinkedinUrl canonicalizes a LinkedIn company/showcase/school URL.
func CleanCompanyLinkedinUrl(linkedinUrlStr string) (string, error) {
	linkedinUrlStr = strings.ToLower(strings.TrimSpace(linkedinUrlStr))
	urlStrParamFree, err := RemoveQueryParamsFromUrl(linkedinUrlStr)
	if err != nil {
		return "", err
	}
	id := LinkedinCompanyUrlToLinkedinId(urlStrParamFree)
	if id == "" {
		return "", errors.New("invalid linkedin url")
	} else if strings.Contains(linkedinUrlStr, "linkedin.com/company") {
		return "https://www.linkedin.com/company/" + id, nil
	} else if strings.Contains(linkedinUrlStr, "linkedin.com/showcase") {
		return "https://www.linkedin.com/showcase/" + id, nil
	} else if strings.Contains(linkedinUrlStr, "linkedin.com/school") {
		return "https://www.linkedin.com/school/" + id, nil
	}
	return "", errors.New("invalid company linkedin url")
}

// BytesToSHA256 returns the lowercase hex-encoded SHA-256 digest of input.
func BytesToSHA256(input []byte) string {
	h := sha256.New()
	h.Write(input)
	return hex.EncodeToString(h.Sum(nil))
}
