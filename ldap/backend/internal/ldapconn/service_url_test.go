// service_url_test.go：连接表单 → profile.URL 组装单测（manifest §4
// host/port/tls_mode 三字段；旧连接的完整 URL 透传兼容）。不发起网络连接。
package ldapconn

import (
	"net/url"
	"strings"
	"testing"
)

func TestBuildLDAPURLStructuredFields(t *testing.T) {
	cases := []struct {
		name           string
		host           string
		tlsMode        string
		legacyStartTLS bool
		wantURL        string
		wantStartTLS   bool
	}{
		{"ldaps mode", "dc-apac.corp.int.kn", "ldaps", false, "ldaps://dc-apac.corp.int.kn", false},
		{"none mode default scheme", "dc-apac.corp.int.kn", "none", false, "ldap://dc-apac.corp.int.kn", false},
		{"empty mode falls back to none", "dc-apac.corp.int.kn", "", false, "ldap://dc-apac.corp.int.kn", false},
		{"starttls keeps ldap scheme", "ldap.example.org", "starttls", false, "ldap://ldap.example.org", true},
		{"tls_mode is case insensitive", "ldap.example.org", "LDAPS", false, "ldaps://ldap.example.org", false},
		{"bare ipv6 literal is bracketed", "::1", "ldaps", false, "ldaps://[::1]", false},
		{"legacy full ldaps url passthrough", "ldaps://dc-apac.corp.int.kn:636", "none", false, "ldaps://dc-apac.corp.int.kn:636", false},
		{"legacy url honors use_starttls", "ldap://ldap.example.org:389", "", true, "ldap://ldap.example.org:389", true},
		{"legacy ldapi socket passthrough", "ldapi:///opt/bitnami/openldap/var/run/ldapi", "none", false, "ldapi:///opt/bitnami/openldap/var/run/ldapi", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotURL, gotStartTLS, err := buildLDAPURL(tc.host, tc.tlsMode, tc.legacyStartTLS)
			if err != nil {
				t.Fatalf("buildLDAPURL(%q, %q) error: %v", tc.host, tc.tlsMode, err)
			}
			if gotURL != tc.wantURL || gotStartTLS != tc.wantStartTLS {
				t.Fatalf("buildLDAPURL(%q, %q) = (%q, %v), want (%q, %v)",
					tc.host, tc.tlsMode, gotURL, gotStartTLS, tc.wantURL, tc.wantStartTLS)
			}
			parsed, err := url.Parse(gotURL)
			if err != nil {
				t.Fatalf("assembled url %q is not parsable: %v", gotURL, err)
			}
			if parsed.Hostname() == "" && parsed.Scheme != "ldapi" {
				t.Fatalf("assembled url %q has no hostname", gotURL)
			}
		})
	}
}

// TestNewProfileFromLifecycleStructuredTLS 钉住回归：tls_mode 必须经由
// NewProfileFromLifecycle 生效（此前 UseStartTLS 被旧 use_starttls 字段的
// 缺省值二次覆盖，starttls 会被静默降级成明文）。
func TestNewProfileFromLifecycleStructuredTLS(t *testing.T) {
	params, err := parseLifecycleForTest(`{
	  "connection": {
	    "id": "c-stls",
	    "host": "ldap.example.org",
	    "external_config": {"auth_type": "simple", "tls_mode": "starttls"}
	  }
	}`)
	if err != nil {
		t.Fatal(err)
	}
	profile, _, err := NewProfileFromLifecycle(params)
	if err != nil {
		t.Fatal(err)
	}
	if profile.URL != "ldap://ldap.example.org" || !profile.UseStartTLS {
		t.Fatalf("profile = (%q, startTLS=%v), want (ldap://ldap.example.org, true)", profile.URL, profile.UseStartTLS)
	}
}

func TestBuildLDAPURLErrors(t *testing.T) {
	cases := []struct {
		name    string
		host    string
		tlsMode string
		wantErr string
	}{
		{"empty host", "  ", "ldaps", "connection host is required"},
		{"host with port points to port field", "dc-apac.corp.int.kn:636", "ldaps", "use the port field"},
		{"unsupported scheme in host binding", "http://ldap.example.org", "none", `unsupported ldap url scheme "http"`},
		{"unsupported tls mode", "ldap.example.org", "tls", `unsupported tls mode "tls"`},
		{"invalid hostname characters", "dc apc", "none", "invalid character"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := buildLDAPURL(tc.host, tc.tlsMode, false)
			if err == nil {
				t.Fatalf("buildLDAPURL(%q, %q) expected error containing %q", tc.host, tc.tlsMode, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want containing %q", err.Error(), tc.wantErr)
			}
		})
	}
}
