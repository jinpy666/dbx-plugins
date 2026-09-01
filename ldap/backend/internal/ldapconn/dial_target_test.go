// dial_target_test.go（L-A 路）：拨号目标归一化单测。
//
// 覆盖 normalizeDialHost：宿主直连路径把 connection.host 原文透传进
// runtime.host，而 LDAP 的 host 绑定是完整 ldap/ldaps URL，必须剥离成
// 裸主机名后再 net.JoinHostPort（回归：ldaps://[ldaps:%2F%2F…]:636）。
// 本文件不发起网络连接。
package ldapconn

import (
	"net"
	"net/url"
	"strconv"
	"testing"
)

func TestNormalizeDialHost(t *testing.T) {
	cases := []struct {
		name     string
		host     string
		port     int
		wantHost string
		wantPort int
	}{
		{"bare host passes through", "dc-apac.corp.int.kn", 636, "dc-apac.corp.int.kn", 636},
		{"transport endpoint untouched", "127.0.0.1", 6360, "127.0.0.1", 6360},
		{"ipv6 literal untouched", "::1", 389, "::1", 389},
		{"strips ldaps url", "ldaps://dc-apac.corp.int.kn:636", 636, "dc-apac.corp.int.kn", 636},
		{"ldaps url derives port when zero", "ldaps://dc-apac.corp.int.kn:636", 0, "dc-apac.corp.int.kn", 636},
		{"ldap url derives default port", "ldap://ldap.example.org", 0, "ldap.example.org", 389},
		{"ipv6 url strips brackets", "ldap://[::1]:389", 0, "::1", 389},
		{"unparseable url passthrough", "http://[::1", 636, "http://[::1", 636},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotHost, gotPort := normalizeDialHost(tc.host, tc.port)
			if gotHost != tc.wantHost || gotPort != tc.wantPort {
				t.Fatalf("normalizeDialHost(%q, %d) = (%q, %d), want (%q, %d)",
					tc.host, tc.port, gotHost, gotPort, tc.wantHost, tc.wantPort)
			}
		})
	}
}

// TestNormalizeDialHostKeepsJoinHostParsable 钉住回归场景：归一化后的
// 拨号目标必须能重新拼出可解析的 ldap URL（此前产出
// ldaps://[ldaps:%2F%2Fdc-apac…:636]:636，url.Parse 报 invalid URL escape）。
func TestNormalizeDialHostKeepsJoinHostParsable(t *testing.T) {
	const ldapURL = "ldaps://dc-apac.corp.int.kn:636"
	host, port := normalizeDialHost(ldapURL, 0)
	rebuilt := (&url.URL{Scheme: "ldaps", Host: net.JoinHostPort(host, strconv.Itoa(port))}).String()
	if _, err := url.Parse(rebuilt); err != nil {
		t.Fatalf("rebuilt dial url %q is not parsable: %v", rebuilt, err)
	}
	if rebuilt != ldapURL {
		t.Fatalf("rebuilt dial url = %q, want %q", rebuilt, ldapURL)
	}
}
