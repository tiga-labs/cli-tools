package enrich

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/tidwall/gjson"
	"github.com/tiga-labs/cli-tools/pkg/urlutil"
)

const openAIChatEndpoint = "https://api.openai.com/v1/chat/completions"

// maxScrapeBytes caps how much of a company homepage is read when scraping for a LinkedIn link.
const maxScrapeBytes = 2 << 20

type openAIChatRequest struct {
	Model          string                `json:"model"`
	Messages       []openAIChatMessage   `json:"messages"`
	ResponseFormat *openAIResponseFormat `json:"response_format,omitempty"`
}

type openAIChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIResponseFormat struct {
	Type string `json:"type"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// CleanFirstAndLastName uses OpenAI (gpt-5.4-mini) to strip middle names, credentials (MD, PhD, CPA),
// and emojis, and fix capitalization on a person's first and last name.
func CleanFirstAndLastName(ctx context.Context, apiKey, firstName, lastName string) (string, string, error) {
	if ctx == nil {
		return "", "", errors.New("ctx is nil")
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return "", "", errors.New("openai API key is not configured")
	}
	firstName = strings.TrimSpace(firstName)
	lastName = strings.TrimSpace(lastName)
	if firstName == "" && lastName == "" {
		return "", "", nil
	}

	prompt := fmt.Sprintf(`Clean the following first and last names by removing middle name and any credentials (like MD, PhD, CPA, etc.) and emojis and correct capitalization.

Return the result as a JSON object with two fields:
"cleaned_first_name": The cleaned first name.
"cleaned_last_name": The cleaned last name.

### Examples:
Input:
{"first_name": "👨‍⚕️ JOHN", "last_name": "DOE, PhD"}
Output:
{"cleaned_first_name": "John", "cleaned_last_name": "Doe"}

Input:
{"first_name": "William D", "last_name": "Johnson III"}
Output:
{"cleaned_first_name": "William", "cleaned_last_name": "Johnson"}

### Input:
{"first_name": %q, "last_name": %q}

### Output:`, firstName, lastName)

	content, err := callOpenAIJSON(ctx, apiKey, prompt)
	if err != nil {
		return "", "", err
	}

	cleanedFirst := strings.TrimSpace(gjson.Get(content, "cleaned_first_name").String())
	if cleanedFirst == "" {
		cleanedFirst = strings.TrimSpace(gjson.Get(content, "first_name").String())
	}
	cleanedLast := strings.TrimSpace(gjson.Get(content, "cleaned_last_name").String())
	if cleanedLast == "" {
		cleanedLast = strings.TrimSpace(gjson.Get(content, "last_name").String())
	}

	if cleanedFirst == "" {
		cleanedFirst = firstName
	}
	if cleanedLast == "" {
		cleanedLast = lastName
	}
	return cleanedFirst, cleanedLast, nil
}

// CleanAccountName uses OpenAI (gpt-5.4-mini) to strip legal suffixes (Inc, LLC, Corp, etc.)
// and fix capitalization on a company name.
func CleanAccountName(ctx context.Context, apiKey, accountName string) (string, error) {
	if ctx == nil {
		return "", errors.New("ctx is nil")
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return "", errors.New("openai API key is not configured")
	}
	accountName = strings.TrimSpace(accountName)
	if accountName == "" {
		return "", nil
	}

	prompt := fmt.Sprintf(`Clean the following company name by removing leading or trailing abbreviations (like Co, Inc, Ltd, LLC, etc.) that refer to legal terms and correct capitalization.

Return the result as a JSON object with one field:
"cleaned_company_name": The cleaned company name.

### Examples:
Input:
{"company_name": "GOOGLE INC"}
Output:
{"cleaned_company_name": "Google"}

Input:
{"company_name": "Ford Motor Company"}
Output:
{"cleaned_company_name": "Ford"}

### Input:
{"company_name": %q}

### Output:`, accountName)

	content, err := callOpenAIJSON(ctx, apiKey, prompt)
	if err != nil {
		return "", err
	}

	cleaned := strings.TrimSpace(gjson.Get(content, "cleaned_company_name").String())
	if cleaned == "" {
		cleaned = strings.TrimSpace(gjson.Get(content, "company_name").String())
	}
	if cleaned == "" {
		return accountName, nil
	}
	return cleaned, nil
}

func callOpenAIJSON(ctx context.Context, apiKey, prompt string) (string, error) {
	reqBody := openAIChatRequest{
		Model: "gpt-5.4-mini",
		Messages: []openAIChatMessage{
			{Role: "user", Content: prompt},
		},
		ResponseFormat: &openAIResponseFormat{Type: "json_object"},
	}

	var chatRes openAIChatResponse
	resp, err := newProviderClient().R().
		SetContext(ctx).
		SetAuthToken(apiKey).
		SetHeader("Content-Type", "application/json").
		SetBody(&reqBody).
		SetResult(&chatRes).
		Post(openAIChatEndpoint)
	if err != nil {
		return "", fmt.Errorf("openai request failed: %w", err)
	}
	if resp.StatusCode() < 200 || resp.StatusCode() >= 300 {
		return "", fmt.Errorf("openai non-2xx status %d: %s", resp.StatusCode(), resp.Body())
	}
	if len(chatRes.Choices) == 0 {
		if raw := resp.Body(); len(raw) > 0 {
			if err := json.Unmarshal(raw, &chatRes); err != nil || len(chatRes.Choices) == 0 {
				return "", errors.New("empty choices from openai")
			}
		} else {
			return "", errors.New("empty response from openai")
		}
	}
	return chatRes.Choices[0].Message.Content, nil
}

// ScrapeCompanyLinkedInFromWebsite fetches a company website homepage and extracts a LinkedIn company link if present.
func ScrapeCompanyLinkedInFromWebsite(ctx context.Context, websiteOrDomain string) (string, error) {
	website := strings.TrimSpace(websiteOrDomain)
	if website == "" {
		return "", nil
	}
	if !strings.HasPrefix(website, "http://") && !strings.HasPrefix(website, "https://") {
		website = "https://" + website
	}

	transport := http.DefaultTransport
	if ProviderTransport != nil {
		transport = ProviderTransport
	}
	client := &http.Client{
		Timeout:   15 * time.Second,
		Transport: transport,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, website, nil)
	if err != nil {
		return "", err
	}

	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", nil
	}

	// Cap the body so a huge or hostile page cannot exhaust memory; LinkedIn links sit in the
	// header/footer of normal homepages, well within this limit.
	doc, err := goquery.NewDocumentFromReader(io.LimitReader(res.Body, maxScrapeBytes))
	if err != nil {
		return "", err
	}

	var found string
	doc.Find("a").Each(func(_ int, s *goquery.Selection) {
		href, ok := s.Attr("href")
		if !ok {
			return
		}
		lower := strings.ToLower(href)
		if strings.Contains(lower, "linkedin.com/company/") ||
			strings.Contains(lower, "linkedin.com/showcase/") ||
			strings.Contains(lower, "linkedin.com/school/") {
			found = href
		}
	})

	if found != "" {
		if cleaned, cleanErr := urlutil.CleanCompanyLinkedinUrl(found); cleanErr == nil {
			return cleaned, nil
		}
		return found, nil
	}
	return "", nil
}
