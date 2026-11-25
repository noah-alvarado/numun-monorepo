package handlers

import "testing"

func TestBackgroundGuideRef(t *testing.T) {
	for _, ok := range []string{"", "  ", "/content/background-guides/numun-xxiv/unsc.md", " /content/background-guides/numun-xxii/african-union.md "} {
		if _, err := backgroundGuideRef(ok); err != nil {
			t.Errorf("%q: unexpected error %v", ok, err)
		}
	}
	for _, bad := range []string{"unsc.md", "/content/background-guides/unsc.md", "/content/background-guides/numun-xxiv/unsc.pdf", "https://numun.org/committees/numun-xxiv/unsc", "/content/background-guides/../uploads/x.md"} {
		if _, err := backgroundGuideRef(bad); err == nil {
			t.Errorf("%q: accepted", bad)
		}
	}
}
