package main

import (
	"encoding/json"
	"testing"
)

func TestNotificationKind(t *testing.T) {
	cases := map[string]struct {
		raw       string
		wantKind  string
		wantEmail string
	}{
		"identity notification": {
			raw:       `{"notificationType":"Bounce","mail":{"messageId":"m1"},"bounce":{"bounceType":"Permanent","bouncedRecipients":[{"emailAddress":"a@example.com"}]}}`,
			wantKind:  "Bounce",
			wantEmail: "a@example.com",
		},
		"configuration-set event": {
			raw:       `{"eventType":"Bounce","mail":{"messageId":"m2"},"bounce":{"bounceType":"Permanent","bouncedRecipients":[{"emailAddress":"b@example.com"}]}}`,
			wantKind:  "Bounce",
			wantEmail: "b@example.com",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var n sesNotification
			if err := json.Unmarshal([]byte(tc.raw), &n); err != nil {
				t.Fatal(err)
			}
			if got := n.kind(); got != tc.wantKind {
				t.Fatalf("kind = %q, want %q", got, tc.wantKind)
			}
			if n.Bounce == nil || n.Bounce.BouncedRecipients[0].EmailAddress != tc.wantEmail {
				t.Fatalf("bounce recipients not parsed: %+v", n.Bounce)
			}
		})
	}
}
