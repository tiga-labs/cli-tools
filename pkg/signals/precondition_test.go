package signals

import (
	"testing"
	"time"
)

func TestAssessPreconditionText(t *testing.T) {
	f := map[string]string{"Title": "VP Sales"}
	if !AssessPrecondition(f, Precondition{Property: "Title", PropertyType: "text", Operator: "ILIKE", Value: "Sales"}) {
		t.Fatal("ILIKE Sales should match")
	}
	if AssessPrecondition(f, Precondition{Property: "Title", PropertyType: "text", Operator: "ILIKE", Value: "sales"}) {
		t.Fatal("ILIKE is case-sensitive, matching gandalf")
	}
	if AssessPrecondition(f, Precondition{Property: "Missing", PropertyType: "text", Operator: "IS_NOT_EMPTY"}) {
		t.Fatal("missing field is empty")
	}
	if !AssessPrecondition(f, Precondition{Property: "{{.Title}}", PropertyType: "text", Operator: "=", Value: "VP Sales"}) {
		t.Fatal("merge-field brackets should be stripped")
	}
}

func TestAssessPreconditionNumber(t *testing.T) {
	p := Precondition{Property: "Employees", PropertyType: "number", Operator: ">=", Value: "100"}
	if !AssessPrecondition(map[string]string{"Employees": "250"}, p) {
		t.Fatal("250 >= 100")
	}
	if AssessPrecondition(map[string]string{"Employees": "n/a"}, p) {
		t.Fatal("unparseable number must be false")
	}
}

func TestAssessPreconditionBoolean(t *testing.T) {
	p := Precondition{Property: "IsCustomer", PropertyType: "boolean", Operator: "IS TRUE"}
	if !AssessPrecondition(map[string]string{"IsCustomer": "TRUE"}, p) {
		t.Fatal("TRUE IS TRUE")
	}
	if AssessPrecondition(map[string]string{"IsCustomer": "false"}, p) {
		t.Fatal("false IS TRUE")
	}
}

func TestAssessPreconditionDateThreeMonths(t *testing.T) {
	p := Precondition{Property: "FundedOn", PropertyType: "date", Operator: "THREE_MONTHS"}
	day := func(daysAgo int) map[string]string {
		return map[string]string{"FundedOn": time.Now().AddDate(0, 0, -daysAgo).Format("2006-01-02")}
	}
	if !AssessPrecondition(day(30), p) {
		t.Fatal("30 days ago is within THREE_MONTHS")
	}
	if AssessPrecondition(day(200), p) {
		t.Fatal("200 days ago is outside THREE_MONTHS")
	}
	if AssessPrecondition(map[string]string{"FundedOn": "last spring"}, p) {
		t.Fatal("unparseable date must be false")
	}
}
