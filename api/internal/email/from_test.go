package email

import (
	"testing"

	"github.com/numun/numun/api/internal/domain"
)

func TestFromHeader(t *testing.T) {
	cases := []struct {
		kind domain.EmailKind
		addr string
		want string
	}{
		{domain.EmailKindAnnouncement, "announcements@mail.test.numun.org", `"NUMUN" <announcements@mail.test.numun.org>`},
		{domain.EmailKind("delegation_approved"), "noreply@mail.test.numun.org", `"NUMUN Portal" <noreply@mail.test.numun.org>`},
	}
	for _, tc := range cases {
		if got := fromHeader(tc.kind, tc.addr); got != tc.want {
			t.Errorf("fromHeader(%q) = %s, want %s", tc.kind, got, tc.want)
		}
	}
}
