package signals

import "encoding/json"

// Status is a signal run's outcome. The codes match the Tiga API.
type Status int

const (
	StatusSuccess             Status = 1 // ran and found a result; read Value
	StatusNotFound            Status = 2 // ran, but no matching data
	StatusMissingDependencies Status = 3 // required inputs were absent
	StatusFailed              Status = 4 // execution errored
	StatusPreconditionFailed  Status = 5 // a precondition blocked execution
)

func (s Status) String() string {
	switch s {
	case StatusSuccess:
		return "success"
	case StatusNotFound:
		return "not_found"
	case StatusMissingDependencies:
		return "missing_dependencies"
	case StatusFailed:
		return "failed"
	case StatusPreconditionFailed:
		return "precondition_failed"
	}
	return "unknown"
}

// Source is a piece of evidence behind a result.
type Source struct {
	URL   string `json:"url"`
	Title string `json:"title,omitempty"`
}

// Result is what every signal type returns for one record.
type Result struct {
	Value   string   `json:"value"`
	Status  Status   `json:"status"`
	Sources []Source `json:"sources"`
	Error   string   `json:"error,omitempty"`
}

// Found reports whether the signal hit.
func (r Result) Found() bool {
	return r.Status == StatusSuccess
}

// MarshalJSON always emits sources as an array, never null.
func (r Result) MarshalJSON() ([]byte, error) {
	type plain Result
	if r.Sources == nil {
		r.Sources = []Source{}
	}
	return json.Marshal(plain(r))
}
