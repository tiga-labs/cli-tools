// Package signals defines AI signals (what to compute for a person or account) and the result
// model every signal type returns. A definition uses the Tiga API's create-signal body, so the
// signals skill's reference examples load unchanged.
package signals

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Signal types supported by the signals CLI.
const (
	TypeGPT            = "gpt"
	TypeStartedNewRole = "started_new_role"
	TypeLinkedInPosts  = "linkedin_posts"
	TypeHiredExecutive = "hired_executive"
	TypeHiringForRole  = "hiring_for_role"
)

// SupportedTypes lists the signal types this CLI can run, in display order.
var SupportedTypes = []string{TypeGPT, TypeStartedNewRole, TypeLinkedInPosts, TypeHiredExecutive, TypeHiringForRole}

// maxJobTitles matches the Tiga API's hiring_for_role limit.
const maxJobTitles = 10

// Precondition gates a signal on a merge-field value (same shape as the Tiga API).
type Precondition struct {
	AssetType    string `json:"asset_type"`
	Property     string `json:"property"`
	PropertyType string `json:"property_type"`
	Operator     string `json:"operator"`
	Value        string `json:"value"`
}

// Config is the subset of the Tiga API computed_config the CLI understands. Unknown fields
// (api, email_settings, signal_found_color, default_value, ...) are ignored on load.
type Config struct {
	Type                       string   `json:"type"`
	Description                string   `json:"description"`
	Prompt                     string   `json:"prompt"`
	Model                      string   `json:"model"`
	ResponseType               string   `json:"response_type"`
	CanUseWebSearch            *bool    `json:"can_use_web_search"` // nil = on
	MaxTokens                  int      `json:"max_tokens"`
	Temperature                float32  `json:"temperature"`
	WordLimit                  int      `json:"word_limit"`
	FewShotExamples            string   `json:"few_shot_examples"`
	ShouldClassifyOutputAsBool bool     `json:"should_classify_output_as_bool"`
	AdvancedModelVerify        bool     `json:"advanced_model_verify"`
	IsAccountInsight           bool     `json:"is_account_insight"`
	RequiredDependencies       []string `json:"required_dependencies"`

	HasPrecondition bool         `json:"has_precondition"`
	Precondition    Precondition `json:"precondition"`

	DateRange        string `json:"date_range"`
	ExpirationInDays int    `json:"expiration_in_days"`

	HiringForRole struct {
		JobTitles []string `json:"job_titles"`
		Location  string   `json:"location"`
	} `json:"hiring_for_role"`

	StartedNewRoleConfig struct {
		StartedNewJobWithin       string `json:"started_new_job_within"`
		ShouldExcludeAdvisorRoles bool   `json:"should_exclude_advisor_roles"`
	} `json:"started_new_role_config"`
}

// Signal is one signal definition: a label plus its computed_config.
type Signal struct {
	Label  string `json:"label"`
	Config Config `json:"computed_config"`
}

// Parse decodes a Tiga signal body, then normalizes and validates it.
func Parse(data []byte) (Signal, error) {
	var s Signal
	if err := json.Unmarshal(data, &s); err != nil {
		return Signal{}, fmt.Errorf("invalid signal definition: %w", err)
	}
	if strings.TrimSpace(s.Config.Type) == "" {
		return Signal{}, errors.New(`definition must be a Tiga signal body {"label": ..., "computed_config": {...}}; computed_config.type is missing`)
	}
	s.Normalize()
	if err := s.Validate(); err != nil {
		return Signal{}, err
	}
	return s, nil
}

// LoadFile reads and parses a signal definition file.
func LoadFile(path string) (Signal, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Signal{}, fmt.Errorf("%s: %w", path, err)
	}
	s, err := Parse(data)
	if err != nil {
		return Signal{}, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// Normalize fills defaults and replaces RequiredDependencies with the derived set.
func (s *Signal) Normalize() {
	s.Config.Type = strings.TrimSpace(s.Config.Type)
	if strings.TrimSpace(s.Label) == "" {
		s.Label = s.Config.Type
	}
	if s.Config.ExpirationInDays <= 0 {
		s.Config.ExpirationInDays = 30
	}
	if s.Config.Type == TypeStartedNewRole && s.Config.StartedNewRoleConfig.StartedNewJobWithin == "" {
		s.Config.StartedNewRoleConfig.StartedNewJobWithin = "THIS_YEAR"
	}
	s.Config.RequiredDependencies = DeriveDependencies(s.Config)
}

// Validate checks the definition against the CLI's supported types and their rules.
func (s Signal) Validate() error {
	c := s.Config
	if !slices.Contains(SupportedTypes, c.Type) {
		return fmt.Errorf("signal type %q is not supported by the signals CLI yet (supported: %s)", c.Type, strings.Join(SupportedTypes, ", "))
	}

	switch c.Type {
	case TypeStartedNewRole:
		if c.IsAccountInsight {
			return errors.New("started_new_role is a person signal; set is_account_insight to false")
		}
	case TypeHiredExecutive, TypeHiringForRole:
		if !c.IsAccountInsight {
			return fmt.Errorf("%s is an account signal; set is_account_insight to true", c.Type)
		}
	}

	switch c.Type {
	case TypeGPT, TypeLinkedInPosts, TypeHiredExecutive:
		if strings.TrimSpace(c.Prompt) == "" {
			return fmt.Errorf("%s requires a prompt", c.Type)
		}
	}

	if c.Type == TypeHiringForRole {
		titles := 0
		for _, t := range c.HiringForRole.JobTitles {
			if strings.TrimSpace(t) != "" {
				titles++
			}
		}
		if titles == 0 {
			return errors.New("hiring_for_role requires at least one job title")
		}
		if len(c.HiringForRole.JobTitles) > maxJobTitles {
			return fmt.Errorf("hiring_for_role accepts at most %d job_titles (got %d)", maxJobTitles, len(c.HiringForRole.JobTitles))
		}
	}

	if c.DateRange != "" && !IsWindow(c.DateRange) {
		return fmt.Errorf("invalid date_range %q (expected one of: %s)", c.DateRange, strings.Join(Windows, ", "))
	}
	if c.Type == TypeStartedNewRole && !IsWindow(c.StartedNewRoleConfig.StartedNewJobWithin) {
		return fmt.Errorf("invalid started_new_job_within %q (expected one of: %s)", c.StartedNewRoleConfig.StartedNewJobWithin, strings.Join(Windows, ", "))
	}
	if c.WordLimit < 0 {
		return errors.New("word_limit must be >= 0")
	}
	if c.Temperature < 0 || c.Temperature > 1 {
		return errors.New("temperature must be between 0 and 1")
	}
	if c.HasPrecondition {
		if err := validatePrecondition(c.Precondition); err != nil {
			return err
		}
	}
	return nil
}

var preconditionOperators = map[string][]string{
	"text":    {"IS_NOT_EMPTY", "ILIKE", "NOT ILIKE", "=", "!="},
	"number":  {"=", "!=", ">", "<", ">=", "<="},
	"boolean": {"IS TRUE", "IS FALSE"},
	"date":    Windows,
}

func validatePrecondition(p Precondition) error {
	if strings.TrimSpace(p.Property) == "" {
		return errors.New("precondition.property is required")
	}
	ops, ok := preconditionOperators[p.PropertyType]
	if !ok {
		return fmt.Errorf("invalid precondition.property_type %q (expected text, number, boolean, or date)", p.PropertyType)
	}
	if !slices.Contains(ops, p.Operator) {
		return fmt.Errorf("invalid precondition.operator %q for property_type %s (expected one of: %s)", p.Operator, p.PropertyType, strings.Join(ops, ", "))
	}
	switch {
	case p.PropertyType == "number":
		if _, err := strconv.Atoi(strings.TrimSpace(p.Value)); err != nil {
			return fmt.Errorf("precondition.value %q must be a whole number for property_type number", p.Value)
		}
	case p.PropertyType == "text" && p.Operator != "IS_NOT_EMPTY" && p.Value == "":
		return fmt.Errorf("precondition.value is required for text operator %s", p.Operator)
	}
	return nil
}

var nonSlugChars = regexp.MustCompile(`[^a-z0-9]+`)

// Key is the signal's column key: the label lowercased, with each run of other characters
// collapsed to "_". It is deterministic so a run can detect two signals with the same key.
func (s Signal) Key() string {
	key := strings.Trim(nonSlugChars.ReplaceAllString(strings.ToLower(s.Label), "_"), "_")
	if key == "" {
		return "signal"
	}
	return key
}

// WebSearch reports whether the definition allows web search (default on).
func (s Signal) WebSearch() bool {
	return s.Config.CanUseWebSearch == nil || *s.Config.CanUseWebSearch
}

// mergeFieldPattern matches {{.Field}}, allowing spaces inside the braces ({{ .Field }}).
var mergeFieldPattern = regexp.MustCompile(`\{\{\s*\.([^{}]+?)\s*\}\}`)

// DeriveDependencies returns the record fields a signal needs: the LinkedIn URL the
// LinkedIn-data types look up (first), then the prompt's merge fields in first-appearance
// order, deduplicated, without CurrentDate.
func DeriveDependencies(c Config) []string {
	deps := []string{}
	switch c.Type {
	case TypeStartedNewRole:
		deps = append(deps, "LinkedInUrl")
	case TypeHiredExecutive, TypeHiringForRole:
		deps = append(deps, "AccountLinkedInUrl")
	case TypeLinkedInPosts:
		if c.IsAccountInsight {
			deps = append(deps, "AccountLinkedInUrl")
		} else {
			deps = append(deps, "LinkedInUrl")
		}
	}

	seen := map[string]bool{}
	for _, d := range deps {
		seen[d] = true
	}
	for _, m := range mergeFieldPattern.FindAllStringSubmatch(c.Prompt, -1) {
		field := strings.TrimSpace(m[1])
		if field == "CurrentDate" || seen[field] {
			continue
		}
		seen[field] = true
		deps = append(deps, field)
	}
	return deps
}
