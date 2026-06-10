package email

import (
	"sort"
	"strings"
	"testing"
)

// fixtures gives at least one representative Vars map per kind so we can
// confirm every template renders without missing-key errors. EMAIL.md §3.4
// fixes the required keys per kind; if a key is added there, this map must
// grow in lockstep.
var fixtures = map[string]map[string]any{
	"delegation_approved": {
		"delegationName": "Westside HS",
		"conferenceName": "NUMUN 2026",
		"portalLink":     "https://portal.numun.org/delegation",
	},
	"delegation_rejected": {
		"delegationName": "Westside HS",
		"conferenceName": "NUMUN 2026",
		"reason":         "Late registration; please apply next year.",
	},
	"payment_recorded": {
		"delegationName":      "Westside HS",
		"amountFormatted":     "$120.00",
		"kind":                "payment",
		"newBalanceFormatted": "$380.00",
		"notes":               "Check #1234",
	},
	"bulk_import_committed": {
		"delegationName":  "Westside HS",
		"createCount":     12,
		"updateCount":     3,
		"softDeleteCount": 0,
		"mode":            "full_sync",
	},
	"assignment_run_completed": {
		"conferenceName":  "NUMUN 2026",
		"assignmentCount": 84,
		"objective":       "1247.5",
		"runLink":         "https://portal.numun.org/admin/assignments",
	},
	"scope_role_changed": {
		"actorName":     "Maya Park",
		"changeSummary": "You are now a staff admin.",
	},
	"new_registration_summary": {
		"conferenceName": "NUMUN 2026",
		"delegations": []map[string]any{
			{"name": "Westside HS", "school": "Westside HS", "advisorEmail": "a@example.com", "createdAt": "Feb 15, 2026 at 9:42 AM CT"},
		},
		"additionalCount": 3,
	},
	"announcement": {
		"subject":  "Day-of logistics",
		"bodyHTML": "<p>See you at registration.</p>",
		"bodyText": "See you at registration.",
	},
}

func TestEveryKindRenders(t *testing.T) {
	tpl, err := LoadTemplates()
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}
	kinds := tpl.Kinds()
	sort.Strings(kinds)

	for _, k := range kinds {
		vars, ok := fixtures[k]
		if !ok {
			t.Errorf("no test fixture for kind %q — add one in templates_test.go", k)
			continue
		}
		data := TemplateData{
			RecipientName:  "Dr. Jane Smith",
			Subject:        "",
			NowFormatted:   "Feb 15, 2026 at 9:42 AM CT",
			BrandColor:     "#4E2A84",
			AssetsBaseURL:  "https://assets.numun.org",
			PortalBaseURL:  "https://portal.numun.org",
			UnsubscribeURL: "",
			Kind:           k,
			Vars:           vars,
		}
		r, err := tpl.Render(k, data)
		if err != nil {
			t.Errorf("render %s: %v", k, err)
			continue
		}
		if strings.TrimSpace(r.Subject) == "" {
			t.Errorf("%s: empty subject", k)
		}
		if !strings.Contains(r.HTML, "NUMUN") {
			t.Errorf("%s: html missing brand header", k)
		}
		if strings.TrimSpace(r.Text) == "" {
			t.Errorf("%s: empty plaintext", k)
		}
	}
}

func TestUnsubscribeURLRoundTrip(t *testing.T) {
	url, err := SignedUnsubscribeURL("https://api.numun.org/v1/email/unsubscribe", "test-secret", "user-123")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if !strings.Contains(url, "token=") {
		t.Fatalf("missing token query: %s", url)
	}
	tok := url[strings.Index(url, "token=")+len("token="):]
	payload, err := VerifyUnsubscribeToken("test-secret", tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if payload.UserID != "user-123" {
		t.Fatalf("wrong userId: %s", payload.UserID)
	}
	if _, err := VerifyUnsubscribeToken("wrong-secret", tok); err == nil {
		t.Fatalf("expected signature mismatch")
	}
}
