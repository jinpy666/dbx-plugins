package ldapconn

import (
	"net/url"
	"testing"
)

func TestInactiveTLSFieldsDoNotReadCertificateFiles(t *testing.T) {
	profile := Profile{TLSVerify: true, TLSCAPath: "/missing/old-ca.pem", TLSServerName: "directory.example"}
	config, err := ldapTLSConfig(profile, false, "directory.example")
	if err != nil || config != nil {
		t.Fatalf("plain connection used inactive TLS fields: config=%v err=%v", config, err)
	}
	profile.TLSVerify = false
	config, err = ldapTLSConfig(profile, true, "directory.example")
	if err != nil || config == nil || !config.InsecureSkipVerify || config.RootCAs != nil {
		t.Fatalf("disabled verification must ignore the hidden CA path: config=%v err=%v", config, err)
	}
	profile.TLSVerify = true
	if _, err := ldapTLSConfig(profile, true, "directory.example"); err == nil {
		t.Fatal("enabled verification must validate the custom CA path")
	}
}

func TestConnectionFormAutomaticPortsFollowEncryption(t *testing.T) {
	for _, tc := range []struct {
		mode string
		port int
	}{{"none", 389}, {"starttls", 389}, {"ldaps", 636}} {
		t.Run(tc.mode, func(t *testing.T) {
			address, _, err := buildLDAPURL("directory.example", tc.mode, false)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := url.Parse(address)
			if err != nil {
				t.Fatal(err)
			}
			if got := ldapURLPort(parsed); got != tc.port {
				t.Fatalf("automatic port = %d, want %d", got, tc.port)
			}
		})
	}
}
