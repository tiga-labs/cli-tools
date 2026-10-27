package enrich

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const zeroBounceValidateURL = "https://api.zerobounce.net/v2/validate"

type ZeroBounceResponse struct {
	Address    string `json:"address"`
	Status     string `json:"status"`
	SubStatus  string `json:"sub_status"`
	FreeEmail  bool   `json:"free_email"`
	DidYouMean string `json:"did_you_mean"`
	Error      string `json:"error"`
}

// ZeroBounceValidateEmail validates an email address using the ZeroBounce API.
// Returns true if the status is "valid".
func ZeroBounceValidateEmail(ctx context.Context, apiKey, email string) (bool, error) {
	if ctx == nil {
		return false, errors.New("ctx is nil")
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return false, errors.New("zerobounce API key is not configured")
	}
	email = strings.TrimSpace(email)
	if email == "" {
		return false, errors.New("email is empty")
	}

	var res ZeroBounceResponse
	resp, err := newProviderClient().R().
		SetContext(ctx).
		SetQueryParams(map[string]string{
			"api_key":    apiKey,
			"email":      email,
			"ip_address": "",
		}).
		SetResult(&res).
		Get(zeroBounceValidateURL)
	if err != nil {
		return false, redactKey(err, apiKey)
	}
	if resp.StatusCode() < 200 || resp.StatusCode() >= 300 {
		return false, fmt.Errorf("zerobounce non-2xx status %d: %s", resp.StatusCode(), resp.Body())
	}
	if res.Error != "" {
		return false, fmt.Errorf("zerobounce error: %s", res.Error)
	}

	return strings.EqualFold(res.Status, "valid"), nil
}
