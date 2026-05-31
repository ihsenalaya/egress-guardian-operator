package cilium

import "testing"

func TestWildcardRisk_High(t *testing.T) {
	cases := []string{"*.com", "*.net", "*.org", "*.io"}
	for _, c := range cases {
		if r := WildcardRisk(c); r != "High" {
			t.Errorf("WildcardRisk(%q) = %q, want High", c, r)
		}
	}
}

func TestWildcardRisk_Medium(t *testing.T) {
	cases := []string{"*.amazonaws.com", "*.cloudfront.net", "*.googleapis.com"}
	for _, c := range cases {
		if r := WildcardRisk(c); r != "Medium" {
			t.Errorf("WildcardRisk(%q) = %q, want Medium", c, r)
		}
	}
}

func TestWildcardRisk_Safe(t *testing.T) {
	cases := []string{"api.stripe.com", "", "login.microsoftonline.com"}
	for _, c := range cases {
		if r := WildcardRisk(c); r != "Safe" {
			t.Errorf("WildcardRisk(%q) = %q, want Safe", c, r)
		}
	}
}

func TestIsDirectPublicIP(t *testing.T) {
	if !IsDirectPublicIP("", "8.8.8.8") {
		t.Error("8.8.8.8 should be detected as direct public IP")
	}
	if IsDirectPublicIP("", "192.168.1.1") {
		t.Error("192.168.1.1 should NOT be detected as direct public IP")
	}
	if IsDirectPublicIP("api.stripe.com", "54.1.1.1") {
		t.Error("should NOT be direct public IP when FQDN is set")
	}
	if IsDirectPublicIP("", "10.0.0.1") {
		t.Error("10.x should NOT be direct public IP")
	}
}

func TestIsAllowedInEnforce(t *testing.T) {
	if !IsAllowedInEnforce("api.stripe.com") {
		t.Error("specific FQDN should be allowed in enforce")
	}
	if IsAllowedInEnforce("*.com") {
		t.Error("*.com wildcard should NOT be allowed in enforce")
	}
	if IsAllowedInEnforce("*.amazonaws.com") {
		t.Error("cloud wildcard should NOT be allowed in enforce")
	}
}
