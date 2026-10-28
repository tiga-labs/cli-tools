package signals

import (
	"reflect"
	"strings"
	"testing"
)

func TestFromInlineGPT(t *testing.T) {
	s, err := FromInline(Inline{Type: "gpt", Prompt: "Is {{.AccountName}} hiring?", Account: true})
	if err != nil {
		t.Fatal(err)
	}
	if s.Label != "gpt" || !reflect.DeepEqual(s.Config.RequiredDependencies, []string{"AccountName"}) || !s.Config.IsAccountInsight {
		t.Fatalf("signal = %+v", s)
	}
}

func TestFromInlineHiring(t *testing.T) {
	if _, err := FromInline(Inline{Type: "hiring_for_role", Account: true, JobTitles: []string{"SDR", "AE"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := FromInline(Inline{Type: "hiring_for_role", JobTitles: []string{"SDR"}}); err == nil || !strings.Contains(err.Error(), "account") {
		t.Fatalf("err = %v", err)
	}
}

func TestFromInlineStartedNewRole(t *testing.T) {
	s, err := FromInline(Inline{Type: "started_new_role", StartedWithin: "SIX_MONTHS", ExcludeAdvisorRoles: true})
	if err != nil {
		t.Fatal(err)
	}
	if s.Config.StartedNewRoleConfig.StartedNewJobWithin != "SIX_MONTHS" || !s.Config.StartedNewRoleConfig.ShouldExcludeAdvisorRoles {
		t.Fatalf("config = %+v", s.Config.StartedNewRoleConfig)
	}
}

func TestFromInlineInvalidType(t *testing.T) {
	if _, err := FromInline(Inline{Type: "search", Prompt: "x"}); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("err = %v", err)
	}
}
