package signals

// Inline describes a signal given as CLI flags instead of a definition file. Per-run
// features (bool classification, verify, web search) are run options, not part of it.
type Inline struct {
	Type                string
	Label               string
	Prompt              string
	Model               string
	DateRange           string
	StartedWithin       string
	Location            string
	Account             bool
	WordLimit           int
	JobTitles           []string
	ExactMatch          bool
	ExcludeAdvisorRoles bool
}

// FromInline builds, normalizes, and validates a Signal from inline flags.
func FromInline(in Inline) (Signal, error) {
	s := Signal{Label: in.Label}
	s.Config.Type = in.Type
	s.Config.Prompt = in.Prompt
	s.Config.Model = in.Model
	s.Config.DateRange = in.DateRange
	// hired_executive and hiring_for_role are account-only, so they need no --account.
	s.Config.IsAccountInsight = in.Account || in.Type == TypeHiredExecutive || in.Type == TypeHiringForRole
	s.Config.ExactMatchSearch = in.ExactMatch
	s.Config.WordLimit = in.WordLimit
	s.Config.HiringForRole.JobTitles = in.JobTitles
	s.Config.HiringForRole.Location = in.Location
	s.Config.StartedNewRoleConfig.StartedNewJobWithin = in.StartedWithin
	s.Config.StartedNewRoleConfig.ShouldExcludeAdvisorRoles = in.ExcludeAdvisorRoles
	s.Normalize()
	if err := s.Validate(); err != nil {
		return Signal{}, err
	}
	return s, nil
}
