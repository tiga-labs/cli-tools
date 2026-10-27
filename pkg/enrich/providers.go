package enrich

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tiga-labs/cli-tools/pkg/urlutil"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const ApolloPlaceholderEmail = "email_not_unlocked@domain.com"

// Poll delays (configurable for tests).
var (
	DropcontactPollDelay = 4 * time.Second
	SeamlessPollDelay    = 5 * time.Second
)

func hostFromContact(row *ContactRecord) string {
	if row == nil {
		return ""
	}
	if strings.TrimSpace(row.Domain) != "" {
		if h, err := urlutil.HostFromWebsite(row.Domain); err == nil && h != "" {
			return h
		}
		return strings.TrimSpace(row.Domain)
	}
	if strings.TrimSpace(row.Website) != "" {
		if h, err := urlutil.HostFromWebsite(row.Website); err == nil && h != "" {
			return h
		}
	}
	return ""
}

// redactedError wraps an error whose message has had secrets removed, while
// preserving errors.Is / errors.As against the original.
type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.err }

// redactKey removes apiKey from err's message. Vendors that authenticate via query
// parameters leak the key through *url.Error on transport failures.
func redactKey(err error, apiKey string) error {
	if err == nil || apiKey == "" {
		return err
	}
	msg := err.Error()
	if !strings.Contains(msg, apiKey) {
		return err
	}
	return &redactedError{msg: strings.ReplaceAll(msg, apiKey, "[REDACTED]"), err: err}
}

// ============================================================================
// 1. Findymail
// ============================================================================

type FindymailResponse struct {
	Contact struct {
		Name   string `json:"name"`
		Domain string `json:"domain"`
		Email  string `json:"email"`
	} `json:"contact"`
}

type FindymailPhoneResponse struct {
	Phone string `json:"phone"`
}

func FindymailFindFromName(ctx context.Context, apiKey string, row *ContactRecord) error {
	if ctx == nil || row == nil {
		return errors.New("nil args to FindymailFindFromName")
	}
	domainOrCompany := hostFromContact(row)
	if domainOrCompany == "" {
		domainOrCompany = strings.TrimSpace(row.CompanyName)
	}
	if domainOrCompany == "" {
		return errors.New("no website, domain, or company name for FindymailFindFromName")
	}
	fullName := strings.TrimSpace(row.FirstName + " " + row.LastName)
	if fullName == "" {
		return errors.New("no person name for FindymailFindFromName")
	}

	var res FindymailResponse
	resp, err := newProviderClient().R().
		SetContext(ctx).
		SetAuthToken(apiKey).
		SetHeader("Content-Type", "application/json").
		SetResult(&res).
		SetBody(&map[string]string{
			"name":   fullName,
			"domain": domainOrCompany,
		}).
		Post("https://app.findymail.com/api/search/name")
	if err != nil {
		return err
	}
	if resp.StatusCode() >= 200 && resp.StatusCode() < 300 {
		if row.Email == "" && strings.TrimSpace(res.Contact.Email) != "" {
			row.Email = strings.TrimSpace(res.Contact.Email)
			row.EmailSource = VendorFindymail
		}
		return nil
	} else if resp.StatusCode() == 402 {
		return errors.New("findymail not enough credits")
	} else if resp.StatusCode() == 423 {
		return errors.New("findymail subscription is paused")
	}
	return fmt.Errorf("non-2xx response from findymail (%d): %s", resp.StatusCode(), resp.Body())
}

func FindymailFindFromLinkedIn(ctx context.Context, apiKey string, row *ContactRecord) error {
	if ctx == nil || row == nil {
		return errors.New("nil args to FindymailFindFromLinkedIn")
	}
	if strings.TrimSpace(row.PersonLinkedIn) == "" {
		return errors.New("no linkedin url for FindymailFindFromLinkedIn")
	}

	lidId := row.PersonLinkedIn
	if split := strings.Split(row.PersonLinkedIn, "/in/"); len(split) >= 2 {
		lidId = split[1]
	}

	var res FindymailResponse
	resp, err := newProviderClient().R().
		SetContext(ctx).
		SetAuthToken(apiKey).
		SetHeader("Content-Type", "application/json").
		SetResult(&res).
		SetBody(&map[string]string{
			"linkedin_url": lidId,
		}).
		Post("https://app.findymail.com/api/search/linkedin")
	if err != nil {
		return err
	}
	if resp.StatusCode() >= 200 && resp.StatusCode() < 300 {
		if row.Email == "" && strings.TrimSpace(res.Contact.Email) != "" {
			row.Email = strings.TrimSpace(res.Contact.Email)
			row.EmailSource = VendorFindymail
		}
		return nil
	} else if resp.StatusCode() == 402 {
		return errors.New("findymail not enough credits")
	} else if resp.StatusCode() == 423 {
		return errors.New("findymail subscription is paused")
	}
	return fmt.Errorf("non-2xx response from findymail (%d): %s", resp.StatusCode(), resp.Body())
}

func FindymailFindPhoneFromLinkedIn(ctx context.Context, apiKey string, row *ContactRecord) error {
	if ctx == nil || row == nil {
		return errors.New("nil args to FindymailFindPhoneFromLinkedIn")
	}
	if strings.TrimSpace(row.PersonLinkedIn) == "" {
		return errors.New("no linkedin url for FindymailFindPhoneFromLinkedIn")
	}

	lidId := urlutil.LinkedinUrlToLinkedinId(row.PersonLinkedIn)
	if lidId == "" {
		lidId = row.PersonLinkedIn
	}

	var res FindymailPhoneResponse
	resp, err := newProviderClient().R().
		SetContext(ctx).
		SetAuthToken(apiKey).
		SetHeader("Content-Type", "application/json").
		SetResult(&res).
		SetBody(&map[string]string{
			"linkedin_url": lidId,
		}).
		Post("https://app.findymail.com/api/search/phone")
	if err != nil {
		return err
	}
	if resp.StatusCode() == 200 {
		if row.Phone == "" && strings.TrimSpace(res.Phone) != "" {
			row.Phone = strings.TrimSpace(res.Phone)
			row.PhoneSource = VendorFindymail
		}
		return nil
	} else if resp.StatusCode() == 402 {
		return errors.New("findymail not enough credits")
	} else if resp.StatusCode() == 423 {
		return errors.New("findymail subscription is paused")
	}
	return fmt.Errorf("non-2xx response from findymail phone (%d): %s", resp.StatusCode(), resp.Body())
}

// ============================================================================
// 2. Prospeo
// ============================================================================

type prospeoEnrichRequest struct {
	OnlyVerifiedEmail  bool                     `json:"only_verified_email,omitempty"`
	EnrichMobile       bool                     `json:"enrich_mobile,omitempty"`
	OnlyVerifiedMobile bool                     `json:"only_verified_mobile,omitempty"`
	Data               prospeoEnrichRequestData `json:"data"`
}

type prospeoEnrichRequestData struct {
	FirstName          string `json:"first_name,omitempty"`
	LastName           string `json:"last_name,omitempty"`
	FullName           string `json:"full_name,omitempty"`
	LinkedinURL        string `json:"linkedin_url,omitempty"`
	Email              string `json:"email,omitempty"`
	CompanyName        string `json:"company_name,omitempty"`
	CompanyWebsite     string `json:"company_website,omitempty"`
	CompanyLinkedinURL string `json:"company_linkedin_url,omitempty"`
}

type prospeoResponse struct {
	Error          bool   `json:"error"`
	ErrorCode      string `json:"error_code,omitempty"`
	FreeEnrichment bool   `json:"free_enrichment"`
	Person         struct {
		PersonID    string `json:"person_id"`
		FirstName   string `json:"first_name"`
		LastName    string `json:"last_name"`
		FullName    string `json:"full_name"`
		LinkedinURL string `json:"linkedin_url"`
		Email       struct {
			Status string `json:"status"`
			Email  string `json:"email"`
		} `json:"email"`
		Mobile struct {
			Status string `json:"status"`
			Mobile string `json:"mobile"`
		} `json:"mobile"`
	} `json:"person"`
}

func buildProspeoReqData(row *ContactRecord) prospeoEnrichRequestData {
	companyName := strings.TrimSpace(row.CompanyName)
	if len(companyName) > 75 {
		companyName = companyName[:74]
	}
	return prospeoEnrichRequestData{
		FirstName:          row.FirstName,
		LastName:           row.LastName,
		Email:              row.Email,
		LinkedinURL:        row.PersonLinkedIn,
		CompanyName:        companyName,
		CompanyWebsite:     hostFromContact(row),
		CompanyLinkedinURL: row.CompanyLinkedIn,
	}
}

func ProspeoFindLinkedInURL(ctx context.Context, apiKey string, row *ContactRecord) error {
	if ctx == nil || row == nil {
		return errors.New("nil args to ProspeoFindLinkedInURL")
	}
	reqData := buildProspeoReqData(row)
	if reqData.CompanyWebsite == "" && reqData.CompanyName == "" {
		return errors.New("no website or company name for ProspeoFindLinkedInURL")
	}

	reqBody := prospeoEnrichRequest{Data: reqData}
	var res prospeoResponse
	resp, err := newProviderClient().R().
		SetContext(ctx).
		SetHeader("Content-Type", "application/json").
		SetHeader("X-KEY", apiKey).
		SetResult(&res).
		SetBody(&reqBody).
		Post("https://api.prospeo.io/enrich-person")
	if err != nil {
		return err
	}
	if resp.StatusCode() >= 200 && resp.StatusCode() < 300 {
		if row.PersonLinkedIn == "" && strings.TrimSpace(res.Person.LinkedinURL) != "" {
			row.PersonLinkedIn = strings.TrimSpace(res.Person.LinkedinURL)
			row.PersonLinkedInSource = VendorProspeo
		}
		return nil
	}
	return fmt.Errorf("prospeo error (%d): %s", resp.StatusCode(), resp.Body())
}

func ProspeoFindFromName(ctx context.Context, apiKey string, row *ContactRecord) error {
	if ctx == nil || row == nil {
		return errors.New("nil args to ProspeoFindFromName")
	}
	reqData := buildProspeoReqData(row)
	if reqData.CompanyWebsite == "" && reqData.CompanyName == "" {
		return errors.New("no website or company name for ProspeoFindFromName")
	}

	reqBody := prospeoEnrichRequest{
		OnlyVerifiedEmail: true,
		Data:              reqData,
	}
	var res prospeoResponse
	resp, err := newProviderClient().R().
		SetContext(ctx).
		SetHeader("Content-Type", "application/json").
		SetHeader("X-KEY", apiKey).
		SetResult(&res).
		SetBody(&reqBody).
		Post("https://api.prospeo.io/enrich-person")
	if err != nil {
		return err
	}
	if resp.StatusCode() >= 200 && resp.StatusCode() < 300 {
		if row.Email == "" && res.Person.Email.Status == "VERIFIED" && strings.TrimSpace(res.Person.Email.Email) != "" {
			row.Email = strings.TrimSpace(res.Person.Email.Email)
			row.EmailSource = VendorProspeo
		}
		if row.PersonLinkedIn == "" && strings.TrimSpace(res.Person.LinkedinURL) != "" {
			row.PersonLinkedIn = strings.TrimSpace(res.Person.LinkedinURL)
			row.PersonLinkedInSource = VendorProspeo
		}
		return nil
	}
	return fmt.Errorf("prospeo error (%d): %s", resp.StatusCode(), resp.Body())
}

func ProspeoFindMobile(ctx context.Context, apiKey string, row *ContactRecord) error {
	if ctx == nil || row == nil {
		return errors.New("nil args to ProspeoFindMobile")
	}
	if urlutil.LinkedinUrlToLinkedinId(row.PersonLinkedIn) == "" {
		return fmt.Errorf("valid linkedin url required for ProspeoFindMobile")
	}
	reqData := buildProspeoReqData(row)
	reqBody := prospeoEnrichRequest{
		OnlyVerifiedMobile: true,
		EnrichMobile:       true,
		Data:               reqData,
	}
	var res prospeoResponse
	resp, err := newProviderClient().R().
		SetContext(ctx).
		SetHeader("Content-Type", "application/json").
		SetHeader("X-KEY", apiKey).
		SetResult(&res).
		SetBody(&reqBody).
		Post("https://api.prospeo.io/enrich-person")
	if err != nil {
		return err
	}
	if resp.StatusCode() >= 200 && resp.StatusCode() < 300 {
		if row.Phone == "" && res.Person.Mobile.Status == "VERIFIED" && strings.TrimSpace(res.Person.Mobile.Mobile) != "" {
			row.Phone = strings.TrimSpace(res.Person.Mobile.Mobile)
			row.PhoneSource = VendorProspeo
		}
		return nil
	}
	return fmt.Errorf("prospeo mobile error (%d): %s", resp.StatusCode(), resp.Body())
}

// ============================================================================
// 3. Apollo (Synchronous match, search, and org enrich)
// ============================================================================

type apolloPersonFact struct {
	ID          string `json:"id"`
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	LinkedinURL string `json:"linkedin_url"`
	Title       string `json:"title"`
	Email       string `json:"email"`
	City        string `json:"city"`
	State       string `json:"state"`
	Country     string `json:"country"`
	Phones      []struct {
		SanitizedNumber string `json:"sanitized_number"`
		RawNumber       string `json:"raw_number"`
	} `json:"phone_numbers"`
	EmploymentHistory []struct {
		Title string `json:"title"`
	} `json:"employment_history"`
	Organization struct {
		Name                  string `json:"name"`
		WebsiteURL            string `json:"website_url"`
		PrimaryDomain         string `json:"primary_domain"`
		LinkedinURL           string `json:"linkedin_url"`
		Industry              string `json:"industry"`
		EstimatedNumEmployees int    `json:"estimated_num_employees"`
		City                  string `json:"city"`
		Country               string `json:"country"`
	} `json:"organization"`
}

type apolloMatchResponse struct {
	Person apolloPersonFact `json:"person"`
}

type apolloSearchResponse struct {
	People []apolloPersonFact `json:"people"`
}

type apolloOrgResponse struct {
	Organization struct {
		Name                  string `json:"name"`
		WebsiteURL            string `json:"website_url"`
		PrimaryDomain         string `json:"primary_domain"`
		LinkedinURL           string `json:"linkedin_url"`
		Industry              string `json:"industry"`
		EstimatedNumEmployees int    `json:"estimated_num_employees"`
	} `json:"organization"`
}

func ApolloEnrichPerson(ctx context.Context, apiKey string, row *ContactRecord, callCount int) error {
	if ctx == nil || row == nil {
		return errors.New("nil args to ApolloEnrichPerson")
	}
	callCount++

	domain := hostFromContact(row)
	if domain == "" && strings.Contains(row.Email, "@") {
		parts := strings.Split(row.Email, "@")
		if len(parts) == 2 {
			domain = parts[1]
		}
	}

	var ret apolloMatchResponse
	req := newProviderClient().R().
		SetContext(ctx).
		SetHeader("Content-Type", "application/json").
		SetHeader("Cache-Control", "no-cache").
		SetHeader("X-Api-Key", apiKey).
		SetResult(&ret)

	if row.Email != "" {
		req = req.SetQueryParam("hashed_email", urlutil.BytesToSHA256([]byte(row.Email)))
	}
	if domain != "" {
		req = req.SetQueryParam("domain", domain)
	}
	if row.FirstName != "" && row.LastName != "" {
		req = req.SetQueryParam("first_name", row.FirstName)
		req = req.SetQueryParam("last_name", row.LastName)
		req = req.SetQueryParam("name", row.FirstName+" "+row.LastName)
	}
	if row.PersonLinkedIn != "" {
		req = req.SetQueryParam("linkedin_url", row.PersonLinkedIn)
	}
	if row.CompanyName != "" {
		req = req.SetQueryParam("organization_name", row.CompanyName)
	}

	resp, err := req.Post("https://api.apollo.io/v1/people/match")
	if err != nil {
		return err
	}

	if resp.StatusCode() == 429 {
		wait, err := strconv.Atoi(resp.Header().Get("Retry-After"))
		if err != nil || wait <= 0 {
			wait = 5
		}
		if wait > 600 || callCount > 5 {
			return errors.New("apollo rate limit exceeded")
		}
		select {
		case <-time.After(time.Duration(wait) * time.Second):
		case <-ctx.Done():
			return ctx.Err()
		}
		return ApolloEnrichPerson(ctx, apiKey, row, callCount)
	}
	if resp.StatusCode() < 200 || resp.StatusCode() >= 300 {
		return fmt.Errorf("apollo match non-2xx (%d): %s", resp.StatusCode(), resp.Body())
	}

	applyApolloPersonToRow(ret.Person, row)
	return nil
}

func ApolloSearchPerson(ctx context.Context, apiKey string, row *ContactRecord, callCount int) error {
	if ctx == nil || row == nil {
		return errors.New("nil args to ApolloSearchPerson")
	}
	if strings.TrimSpace(row.FirstName) == "" || strings.TrimSpace(row.LastName) == "" || strings.TrimSpace(row.CompanyName) == "" {
		return nil
	}
	callCount++

	var res apolloSearchResponse
	keywords := strings.TrimSpace(row.FirstName + " " + row.LastName + " " + row.CompanyName)
	resp, err := newProviderClient().R().
		SetContext(ctx).
		SetHeader("Content-Type", "application/json").
		SetHeader("Cache-Control", "no-cache").
		SetHeader("X-Api-Key", apiKey).
		SetQueryParam("q_keywords", keywords).
		SetQueryParam("page", "1").
		SetQueryParam("per_page", "1").
		SetResult(&res).
		Post("https://api.apollo.io/v1/mixed_people/search")
	if err != nil {
		return err
	}

	if resp.StatusCode() == 429 {
		wait, err := strconv.Atoi(resp.Header().Get("Retry-After"))
		if err != nil || wait <= 0 {
			wait = 5
		}
		if wait > 600 || callCount > 5 {
			return errors.New("apollo search rate limit exceeded")
		}
		select {
		case <-time.After(time.Duration(wait) * time.Second):
		case <-ctx.Done():
			return ctx.Err()
		}
		return ApolloSearchPerson(ctx, apiKey, row, callCount)
	}
	if resp.StatusCode() < 200 || resp.StatusCode() >= 300 {
		return fmt.Errorf("apollo search non-2xx (%d): %s", resp.StatusCode(), resp.Body())
	}

	if len(res.People) > 0 {
		applyApolloPersonToRow(res.People[0], row)
	}
	return nil
}

func ApolloEnrichAccount(ctx context.Context, apiKey, domain string) (apolloOrgResponse, error) {
	var res apolloOrgResponse
	if strings.TrimSpace(domain) == "" || strings.TrimSpace(apiKey) == "" {
		return res, nil
	}
	resp, err := newProviderClient().R().
		SetContext(ctx).
		SetHeader("Content-Type", "application/json").
		SetHeader("Cache-Control", "no-cache").
		SetHeader("X-Api-Key", apiKey).
		SetQueryParam("domain", domain).
		SetResult(&res).
		Get("https://api.apollo.io/v1/organizations/enrich")
	if err != nil {
		return res, err
	}
	if resp.StatusCode() < 200 || resp.StatusCode() >= 300 {
		return res, fmt.Errorf("apollo org enrich non-2xx (%d)", resp.StatusCode())
	}
	return res, nil
}

func applyApolloPersonToRow(person apolloPersonFact, row *ContactRecord) {
	if row.FirstName == "" && person.FirstName != "" {
		row.FirstName = person.FirstName
	}
	if row.LastName == "" && person.LastName != "" {
		row.LastName = person.LastName
	}
	if row.Website == "" && person.Organization.WebsiteURL != "" {
		row.Website = person.Organization.WebsiteURL
	}
	if row.Domain == "" && person.Organization.PrimaryDomain != "" {
		row.Domain = person.Organization.PrimaryDomain
	}
	if row.Email == "" && person.Email != "" && !strings.EqualFold(person.Email, ApolloPlaceholderEmail) {
		row.Email = strings.TrimSpace(person.Email)
		row.EmailSource = VendorApollo
	}
	if row.CompanyName == "" && person.Organization.Name != "" {
		row.CompanyName = person.Organization.Name
	}
	if row.Title == "" && person.Title != "" {
		row.Title = person.Title
	} else if row.Title == "" && len(person.EmploymentHistory) > 0 {
		row.Title = person.EmploymentHistory[0].Title
	}
	if urlutil.LinkedinUrlToLinkedinId(row.PersonLinkedIn) == "" && urlutil.LinkedinUrlToLinkedinId(person.LinkedinURL) != "" {
		row.PersonLinkedIn = person.LinkedinURL
		row.PersonLinkedInSource = VendorApollo
	}
	if row.CompanyLinkedIn == "" && person.Organization.LinkedinURL != "" {
		row.CompanyLinkedIn = person.Organization.LinkedinURL
		row.CompanyLinkedInSource = VendorApollo
	}
	if row.Phone == "" && len(person.Phones) > 0 && person.Phones[0].SanitizedNumber != "" {
		row.Phone = person.Phones[0].SanitizedNumber
		row.PhoneSource = VendorApollo
	}
	if row.Industry == "" && person.Organization.Industry != "" {
		row.Industry = person.Organization.Industry
	}
	if row.EmployeeCount == 0 && person.Organization.EstimatedNumEmployees > 0 {
		row.EmployeeCount = person.Organization.EstimatedNumEmployees
	}
}

// ============================================================================
// 4. Lusha
// ============================================================================

type lushaPersonResponse struct {
	RequestId string `json:"requestId"`
	Data      struct {
		FirstName      string `json:"firstName"`
		LastName       string `json:"lastName"`
		JobTitle       string `json:"jobTitle"`
		LinkedinUrl    string `json:"linkedinUrl"`
		CompanyName    string `json:"companyName"`
		CompanyDomain  string `json:"companyDomain"`
		EmailAddresses []struct {
			Email           string `json:"email"`
			Type            string `json:"type"`
			EmailConfidence string `json:"emailConfidence"`
		} `json:"emailAddresses"`
		PhoneNumbers []struct {
			Phone     string `json:"phone"`
			Type      string `json:"type"`
			DoNotCall bool   `json:"doNotCall"`
		} `json:"phoneNumbers"`
	} `json:"data"`
}

var lushaHighConfidence = map[string]bool{
	"a+":   true,
	"a":    true,
	"high": true,
}

func LushaEnrichContact(ctx context.Context, apiKey string, row *ContactRecord, needEmail, needPhone bool) error {
	if ctx == nil || row == nil {
		return errors.New("nil args to LushaEnrichContact")
	}

	queryParams := map[string]string{}
	if urlutil.LinkedinUrlToLinkedinId(row.PersonLinkedIn) != "" {
		queryParams["linkedinUrl"] = row.PersonLinkedIn
	} else if row.Email != "" {
		queryParams["email"] = row.Email
	} else {
		host := hostFromContact(row)
		if row.FirstName == "" || row.LastName == "" || (host == "" && row.CompanyName == "") {
			return errors.New("insufficient identifiers for LushaEnrichContact")
		}
		queryParams["firstName"] = row.FirstName
		queryParams["lastName"] = row.LastName
		if host != "" {
			queryParams["companyDomain"] = host
		} else {
			queryParams["companyName"] = row.CompanyName
		}
	}

	if needEmail && !needPhone {
		queryParams["filterBy"] = "emailAddresses"
	} else if needPhone && !needEmail {
		queryParams["filterBy"] = "phoneNumbers"
	} else if !needEmail && !needPhone {
		queryParams["filterBy"] = "emailAddresses"
	}

	var personRes lushaPersonResponse
	resp, err := newProviderClient().R().
		SetContext(ctx).
		SetHeader("api_key", apiKey).
		SetQueryParams(queryParams).
		SetResult(&personRes).
		Get("https://api.lusha.com/v2/person")
	if err != nil {
		return err
	}
	if resp.StatusCode() == 404 {
		return errors.New("lusha found no match for this contact")
	}
	if resp.StatusCode() < 200 || resp.StatusCode() >= 300 {
		return fmt.Errorf("lusha non-2xx (%d): %s", resp.StatusCode(), resp.Body())
	}

	res := personRes.Data
	if row.Email == "" {
		email := ""
		for _, c := range res.EmailAddresses {
			if c.Email == "" {
				continue
			}
			if lushaHighConfidence[strings.ToLower(c.EmailConfidence)] {
				email = c.Email
				break
			}
			if email == "" {
				email = c.Email
			}
		}
		if email != "" {
			row.Email = strings.TrimSpace(email)
			row.EmailSource = VendorLusha
		}
	}

	if row.Phone == "" {
		phone := ""
		for _, c := range res.PhoneNumbers {
			if c.Phone == "" || c.DoNotCall {
				continue
			}
			if strings.Contains(strings.ToLower(c.Type), "mobile") {
				phone = c.Phone
				break
			}
			if phone == "" {
				phone = c.Phone
			}
		}
		if phone != "" {
			row.Phone = strings.TrimSpace(phone)
			row.PhoneSource = VendorLusha
		}
	}

	if row.PersonLinkedIn == "" && res.LinkedinUrl != "" {
		row.PersonLinkedIn = strings.TrimSpace(res.LinkedinUrl)
		row.PersonLinkedInSource = VendorLusha
	}
	return nil
}

// ============================================================================
// 5. Seamless
// ============================================================================

type seamlessResearchContactInput struct {
	ContactName  string `json:"contactName,omitempty"`
	CompanyName  string `json:"companyName,omitempty"`
	Domain       string `json:"domain,omitempty"`
	Email        string `json:"email,omitempty"`
	LiProfileUrl string `json:"liProfileUrl,omitempty"`
	Title        string `json:"title,omitempty"`
}

type seamlessResearchSubmitResponse struct {
	Success    bool     `json:"success"`
	RequestIds []string `json:"requestIds"`
}

type seamlessResearchedContact struct {
	LIProfileUrl          string `json:"lIProfileUrl"`
	Email1                string `json:"email1"`
	Email1EmailAI         string `json:"email1EmailAI"`
	Email2                string `json:"email2"`
	Email2EmailAI         string `json:"email2EmailAI"`
	Email3                string `json:"email3"`
	Email3EmailAI         string `json:"email3EmailAI"`
	ContactPhone1         string `json:"contactPhone1"`
	ContactPhone1DataType string `json:"contactPhone1DataType"`
	ContactPhone1IsDnc    bool   `json:"contactPhone1IsDnc"`
	ContactPhone2         string `json:"contactPhone2"`
	ContactPhone2DataType string `json:"contactPhone2DataType"`
	ContactPhone2IsDnc    bool   `json:"contactPhone2IsDnc"`
	ContactPhone3         string `json:"contactPhone3"`
	ContactPhone3DataType string `json:"contactPhone3DataType"`
	ContactPhone3IsDnc    bool   `json:"contactPhone3IsDnc"`
}

type seamlessPollResponse struct {
	Success bool `json:"success"`
	Data    []struct {
		RequestId string                    `json:"requestId"`
		Status    string                    `json:"status"`
		Contact   seamlessResearchedContact `json:"contact"`
	} `json:"data"`
}

var seamlessTerminalStatuses = map[string]bool{
	"done":                            true,
	"error":                           true,
	"missing":                         true,
	"duplicate":                       true,
	"not found":                       true,
	"contact-already-researched":      true,
	"No license or credits available": true,
}

func SeamlessResearchContact(ctx context.Context, apiKey string, row *ContactRecord) error {
	if ctx == nil || row == nil {
		return errors.New("nil args to SeamlessResearchContact")
	}

	input := seamlessResearchContactInput{
		ContactName: strings.TrimSpace(row.FirstName + " " + row.LastName),
		Title:       row.Title,
	}
	if urlutil.LinkedinUrlToLinkedinId(row.PersonLinkedIn) != "" {
		input.LiProfileUrl = row.PersonLinkedIn
	}
	host := hostFromContact(row)
	if host != "" {
		input.Domain = host
	} else if len(row.CompanyName) > 75 {
		input.CompanyName = row.CompanyName[:74]
	} else {
		input.CompanyName = row.CompanyName
	}

	if input.LiProfileUrl == "" && (input.ContactName == "" || (input.Domain == "" && input.CompanyName == "")) {
		return errors.New("insufficient identifiers for SeamlessResearchContact")
	}

	var submitRes seamlessResearchSubmitResponse
	resp, err := newProviderClient().R().
		SetContext(ctx).
		SetHeader("Content-Type", "application/json").
		SetHeader("Token", apiKey).
		SetResult(&submitRes).
		SetBody(map[string]any{"contacts": []seamlessResearchContactInput{input}}).
		Post("https://api.seamless.ai/api/client/v1/contacts/research")
	if err != nil {
		return err
	}
	if resp.StatusCode() < 200 || resp.StatusCode() >= 300 {
		return fmt.Errorf("seamless submit non-2xx (%d): %s", resp.StatusCode(), resp.Body())
	}
	if !submitRes.Success || len(submitRes.RequestIds) == 0 {
		return fmt.Errorf("seamless submit returned no request ids: %s", resp.Body())
	}

	requestID := submitRes.RequestIds[0]
	const maxAttempts = 18
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(SeamlessPollDelay):
		}

		var pollRes seamlessPollResponse
		pResp, pErr := newProviderClient().R().
			SetContext(ctx).
			SetHeader("Content-Type", "application/json").
			SetHeader("Token", apiKey).
			SetQueryParam("requestIds", requestID).
			SetResult(&pollRes).
			Get("https://api.seamless.ai/api/client/v1/contacts/research/poll")
		if pErr != nil {
			return pErr
		}
		if pResp.StatusCode() == 429 {
			continue
		}
		if pResp.StatusCode() < 200 || pResp.StatusCode() >= 300 {
			return fmt.Errorf("seamless poll non-2xx (%d): %s", pResp.StatusCode(), pResp.Body())
		}

		for _, item := range pollRes.Data {
			if item.RequestId != "" && item.RequestId != requestID {
				continue
			}
			if seamlessTerminalStatuses[item.Status] {
				if item.Status != "done" {
					return fmt.Errorf("seamless research returned status %q", item.Status)
				}
				applySeamlessToRow(item.Contact, row)
				return nil
			}
		}
	}
	return fmt.Errorf("seamless polling timed out for request %s", requestID)
}

func applySeamlessToRow(c seamlessResearchedContact, row *ContactRecord) {
	if row.Email == "" {
		candidates := []struct{ email, ai string }{
			{c.Email1, c.Email1EmailAI},
			{c.Email2, c.Email2EmailAI},
			{c.Email3, c.Email3EmailAI},
		}
		for _, cand := range candidates {
			if cand.email != "" && strings.EqualFold(cand.ai, "valid") {
				row.Email = strings.TrimSpace(cand.email)
				row.EmailSource = VendorSeamless
				break
			}
		}
		if row.Email == "" && c.Email1 != "" {
			row.Email = strings.TrimSpace(c.Email1)
			row.EmailSource = VendorSeamless
		}
	}

	if row.Phone == "" {
		phones := []struct {
			phone, dt string
			dnc       bool
		}{
			{c.ContactPhone1, c.ContactPhone1DataType, c.ContactPhone1IsDnc},
			{c.ContactPhone2, c.ContactPhone2DataType, c.ContactPhone2IsDnc},
			{c.ContactPhone3, c.ContactPhone3DataType, c.ContactPhone3IsDnc},
		}
		chosen := ""
		for _, p := range phones {
			if p.phone == "" || p.dnc {
				continue
			}
			if strings.Contains(strings.ToLower(p.dt), "mobile") {
				chosen = p.phone
				break
			}
			if chosen == "" {
				chosen = p.phone
			}
		}
		if chosen != "" {
			row.Phone = strings.TrimSpace(chosen)
			row.PhoneSource = VendorSeamless
		}
	}

	if row.PersonLinkedIn == "" && c.LIProfileUrl != "" {
		row.PersonLinkedIn = strings.TrimSpace(c.LIProfileUrl)
		row.PersonLinkedInSource = VendorSeamless
	}
}

// ============================================================================
// 6. Dropcontact
// ============================================================================

type dropcontactEnrichResponse struct {
	Error     bool   `json:"error"`
	RequestId string `json:"request_id"`
	Success   bool   `json:"success"`
}

type dropcontactResultResponse struct {
	Error   bool `json:"error"`
	Success bool `json:"success"`
	Data    []struct {
		Email []struct {
			Email         string `json:"email"`
			Qualification string `json:"qualification"`
		} `json:"email"`
		Phone           string `json:"phone"`
		MobilePhone     string `json:"mobile_phone"`
		LinkedinUrl     string `json:"linkedin"`
		CompanyLinkedin string `json:"company_linkedin"`
	} `json:"data"`
}

func DropcontactFindFromName(ctx context.Context, apiKey string, row *ContactRecord) error {
	if ctx == nil || row == nil {
		return errors.New("nil args to DropcontactFindFromName")
	}

	host := hostFromContact(row)
	company := host
	if company == "" {
		company = row.CompanyName
		if len(company) > 75 {
			company = company[:74]
		}
	}
	if company == "" && row.FirstName == "" && row.LastName == "" {
		return errors.New("insufficient identifiers for DropcontactFindFromName")
	}

	item := map[string]string{
		"first_name": row.FirstName,
		"last_name":  row.LastName,
		"company":    company,
	}
	if host != "" {
		item["website"] = row.Website
		if item["website"] == "" {
			item["website"] = host
		}
	}
	if row.Title != "" {
		item["job"] = row.Title
	}

	var enrichRes dropcontactEnrichResponse
	resp, err := newProviderClient().R().
		SetContext(ctx).
		SetHeader("Content-Type", "application/json").
		SetHeader("X-Access-Token", apiKey).
		SetResult(&enrichRes).
		SetBody(map[string]any{
			"data":     []map[string]string{item},
			"siren":    false,
			"language": "en",
		}).
		Post("https://api.dropcontact.com/v1/enrich/all")
	if err != nil {
		return err
	}
	if resp.StatusCode() < 200 || resp.StatusCode() >= 300 || !enrichRes.Success {
		return fmt.Errorf("dropcontact submit error (%d): %s", resp.StatusCode(), resp.Body())
	}

	for attempt := 1; attempt <= 20; attempt++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(DropcontactPollDelay):
		}

		var res dropcontactResultResponse
		gResp, gErr := newProviderClient().R().
			SetContext(ctx).
			SetHeader("Content-Type", "application/json").
			SetHeader("X-Access-Token", apiKey).
			SetResult(&res).
			Get(fmt.Sprintf("https://api.dropcontact.com/v1/enrich/all/%s", enrichRes.RequestId))
		if gErr != nil {
			return gErr
		}
		if gResp.StatusCode() < 200 || gResp.StatusCode() >= 300 {
			return fmt.Errorf("dropcontact poll error (%d): %s", gResp.StatusCode(), gResp.Body())
		}
		if !res.Success || len(res.Data) == 0 {
			continue
		}

		d := res.Data[0]
		if row.Email == "" && len(d.Email) > 0 {
			for _, e := range d.Email {
				if e.Qualification == "verified" || e.Qualification == "qualified" {
					row.Email = strings.TrimSpace(e.Email)
					row.EmailSource = VendorDropcontact
					break
				}
			}
			if row.Email == "" {
				row.Email = strings.TrimSpace(d.Email[0].Email)
				row.EmailSource = VendorDropcontact
			}
		}
		if row.Phone == "" && d.MobilePhone != "" {
			row.Phone = strings.TrimSpace(d.MobilePhone)
			row.PhoneSource = VendorDropcontact
		} else if row.Phone == "" && d.Phone != "" {
			row.Phone = strings.TrimSpace(d.Phone)
			row.PhoneSource = VendorDropcontact
		}
		if row.PersonLinkedIn == "" && d.LinkedinUrl != "" {
			row.PersonLinkedIn = strings.TrimSpace(d.LinkedinUrl)
			row.PersonLinkedInSource = VendorDropcontact
		}
		if row.CompanyLinkedIn == "" && d.CompanyLinkedin != "" {
			row.CompanyLinkedIn = strings.TrimSpace(d.CompanyLinkedin)
			row.CompanyLinkedInSource = VendorDropcontact
		}
		return nil
	}

	return fmt.Errorf("dropcontact polling timed out for request %s", enrichRes.RequestId)
}

// ============================================================================
// 7. Hunter
// ============================================================================

type hunterEmailFinderResponse struct {
	Data struct {
		Email        string `json:"email"`
		Score        int    `json:"score"`
		Position     string `json:"position"`
		Verification struct {
			Status string `json:"status"`
		} `json:"verification"`
	} `json:"data"`
	Errors []struct {
		Code    int    `json:"code"`
		Details string `json:"details"`
	} `json:"errors"`
}

func HunterFindFromName(ctx context.Context, apiKey string, row *ContactRecord) error {
	if ctx == nil || row == nil {
		return errors.New("nil args to HunterFindFromName")
	}
	if row.FirstName == "" || row.LastName == "" {
		return errors.New("first_name and last_name are required for Hunter")
	}
	domain := hostFromContact(row)
	if domain == "" && row.CompanyName == "" {
		return errors.New("domain or company name is required for Hunter")
	}

	var res hunterEmailFinderResponse
	req := newProviderClient().R().
		SetContext(ctx).
		SetHeader("Content-Type", "application/json").
		SetQueryParam("api_key", apiKey).
		SetQueryParam("first_name", row.FirstName).
		SetQueryParam("last_name", row.LastName).
		SetResult(&res)

	if domain != "" {
		req.SetQueryParam("domain", domain)
	} else {
		req.SetQueryParam("company", row.CompanyName)
	}

	resp, err := req.Get("https://api.hunter.io/v2/email-finder")
	if err != nil {
		return redactKey(err, apiKey)
	}
	if resp.StatusCode() < 200 || resp.StatusCode() >= 300 {
		return fmt.Errorf("hunter non-2xx (%d): %s", resp.StatusCode(), resp.Body())
	}
	if len(res.Errors) > 0 {
		return fmt.Errorf("hunter error: %s", res.Errors[0].Details)
	}

	if res.Data.Email != "" {
		isVerified := res.Data.Verification.Status == "valid"
		if row.Email == "" && (res.Data.Score >= 50 || isVerified) {
			row.Email = strings.TrimSpace(res.Data.Email)
			row.EmailSource = VendorHunter
		}
		if row.Title == "" && res.Data.Position != "" {
			row.Title = strings.TrimSpace(res.Data.Position)
		}
	}
	return nil
}

// ============================================================================
// 8. Datagma
// ============================================================================

type datagmaFindByNameResponse struct {
	Email  string `json:"email"`
	Status string `json:"status"`
}

type datagmaEnrichPersonResponse struct {
	Person struct {
		Basic struct {
			FirstName   string `json:"firstName"`
			LastName    string `json:"lastName"`
			LinkedInURL string `json:"linkedInUrl"`
		} `json:"basic"`
	} `json:"person"`
}

func DatagmaFindFromName(ctx context.Context, apiKey string, row *ContactRecord) error {
	if ctx == nil || row == nil {
		return errors.New("nil args to DatagmaFindFromName")
	}
	company := hostFromContact(row)
	if company == "" {
		company = strings.TrimSpace(row.CompanyName)
	}
	if company == "" {
		return errors.New("no website, domain, or company name for DatagmaFindFromName")
	}

	var res datagmaFindByNameResponse
	resp, err := newProviderClient().R().
		SetContext(ctx).
		SetHeader("Content-Type", "application/json").
		SetHeader("Cache-Control", "no-cache").
		SetResult(&res).
		SetQueryParams(map[string]string{
			"apiId":     apiKey,
			"firstName": row.FirstName,
			"lastName":  row.LastName,
			"fullName":  strings.TrimSpace(row.FirstName + " " + row.LastName),
			"company":   company,
		}).
		Get("https://gateway.datagma.net/api/ingress/v8/findEmail")
	if err != nil {
		return redactKey(err, apiKey)
	}
	if resp.StatusCode() != 200 {
		return fmt.Errorf("datagma findEmail error (%d): %s", resp.StatusCode(), resp.Body())
	}

	if row.Email == "" && res.Status == "VALID" && strings.TrimSpace(res.Email) != "" {
		row.Email = strings.TrimSpace(res.Email)
		row.EmailSource = VendorDatagma
	}
	return nil
}

func DatagmaEnrichPerson(ctx context.Context, apiKey string, row *ContactRecord) error {
	if ctx == nil || row == nil {
		return errors.New("nil args to DatagmaEnrichPerson")
	}
	if row.FirstName == "" || row.LastName == "" {
		return errors.New("first and last name required for DatagmaEnrichPerson")
	}
	company := hostFromContact(row)
	if company == "" {
		company = strings.TrimSpace(row.CompanyName)
	}
	if company == "" {
		return errors.New("no website, domain, or company name for DatagmaEnrichPerson")
	}

	var res datagmaEnrichPersonResponse
	resp, err := newProviderClient().R().
		SetContext(ctx).
		SetHeader("Content-Type", "application/json").
		SetHeader("Cache-Control", "no-cache").
		SetResult(&res).
		SetQueryParams(map[string]string{
			"apiId":          apiKey,
			"data":           company,
			"firstName":      row.FirstName,
			"lastName":       row.LastName,
			"fullName":       strings.TrimSpace(row.FirstName + " " + row.LastName),
			"companyFull":    "true",
			"companyKeyword": company,
		}).
		Get("https://gateway.datagma.net/api/ingress/v2/full")
	if err != nil {
		return redactKey(err, apiKey)
	}
	if resp.StatusCode() != 200 {
		return fmt.Errorf("datagma enrich error (%d): %s", resp.StatusCode(), resp.Body())
	}

	if row.FirstName == "" && res.Person.Basic.FirstName != "" {
		row.FirstName = res.Person.Basic.FirstName
	}
	if row.LastName == "" && res.Person.Basic.LastName != "" {
		row.LastName = res.Person.Basic.LastName
	}
	if row.PersonLinkedIn == "" && res.Person.Basic.LinkedInURL != "" {
		row.PersonLinkedIn = strings.TrimSpace(res.Person.Basic.LinkedInURL)
		row.PersonLinkedInSource = VendorDatagma
	}
	return nil
}

// ============================================================================
// 9. LeadIQ
// ============================================================================

type leadIQGraphQLResponse struct {
	Data struct {
		SearchPeople struct {
			TotalResults int `json:"totalResults"`
			Results      []struct {
				LinkedIn struct {
					LinkedInURL string `json:"linkedinUrl"`
				} `json:"linkedin"`
				CurrentPositions []struct {
					Title  string `json:"title"`
					Emails []struct {
						Value  string `json:"value"`
						Status string `json:"status"`
					} `json:"emails"`
				} `json:"currentPositions"`
			} `json:"results"`
		} `json:"searchPeople"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func LeadIQFindByNameAndCompany(ctx context.Context, apiKey string, row *ContactRecord) error {
	if ctx == nil || row == nil {
		return errors.New("nil args to LeadIQFindByNameAndCompany")
	}
	company := hostFromContact(row)
	if company == "" {
		company = strings.TrimSpace(row.CompanyName)
	}
	if company == "" {
		return errors.New("no website, domain, or company name for LeadIQFindByNameAndCompany")
	}

	gqlQuery := `query ($input: SearchPeopleInput!) {
		searchPeople(input: $input) {
			totalResults
			results {
				linkedin { linkedinUrl }
				currentPositions {
					title
					emails { value status }
				}
			}
		}
	}`

	payload := map[string]any{
		"query": gqlQuery,
		"variables": map[string]any{
			"input": map[string]any{
				"firstName": row.FirstName,
				"lastName":  row.LastName,
				"limit":     1,
				"company": map[string]any{
					"name": company,
				},
			},
		},
	}

	reqBytes, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.leadiq.com/graphql", strings.NewReader(string(reqBytes)))
	if err != nil {
		return err
	}
	req.SetBasicAuth(apiKey, "")
	req.Header.Set("Content-Type", "application/json")

	transport := http.DefaultTransport
	if ProviderTransport != nil {
		transport = ProviderTransport
	}
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("leadiq non-2xx status %d", resp.StatusCode)
	}

	var gqlRes leadIQGraphQLResponse
	if err := json.NewDecoder(resp.Body).Decode(&gqlRes); err != nil {
		return err
	}
	if len(gqlRes.Errors) > 0 {
		return fmt.Errorf("leadiq graphql error: %s", gqlRes.Errors[0].Message)
	}

	sp := gqlRes.Data.SearchPeople
	if sp.TotalResults >= 1 && len(sp.Results) > 0 {
		r := sp.Results[0]
		if row.Email == "" && len(r.CurrentPositions) > 0 && len(r.CurrentPositions[0].Emails) > 0 {
			val := strings.TrimSpace(r.CurrentPositions[0].Emails[0].Value)
			if val != "" {
				row.Email = val
				row.EmailSource = VendorLeadIQ
			}
		}
		if row.PersonLinkedIn == "" && strings.TrimSpace(r.LinkedIn.LinkedInURL) != "" {
			row.PersonLinkedIn = strings.TrimSpace(r.LinkedIn.LinkedInURL)
			row.PersonLinkedInSource = VendorLeadIQ
		}
	}
	return nil
}
