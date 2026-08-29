package ldapgssapi

import (
	"testing"

	krbgssapi "github.com/jcmturner/gokrb5/v8/gssapi"
)

func TestClientOptionsContextFlags_DefaultsToIntegrityOnly(t *testing.T) {
	flags := (ClientOptions{}).contextFlags()
	if len(flags) != 1 || flags[0] != krbgssapi.ContextFlagInteg {
		t.Fatalf("contextFlags() = %#v, want only ContextFlagInteg", flags)
	}
}

func TestClientOptionsContextFlags_AuthConfMutual(t *testing.T) {
	flags := (ClientOptions{
		UseIntegrity:       true,
		UseConfidentiality: true,
		MutualAuth:         true,
	}).contextFlags()

	if len(flags) != 3 {
		t.Fatalf("contextFlags() len = %d, want 3", len(flags))
	}
	if flags[0] != krbgssapi.ContextFlagInteg || flags[1] != krbgssapi.ContextFlagConf || flags[2] != krbgssapi.ContextFlagMutual {
		t.Fatalf("contextFlags() = %#v", flags)
	}
}

func TestClientOptionsNeedInitContinuation(t *testing.T) {
	if (ClientOptions{}).needInitContinuation() {
		t.Fatal("needInitContinuation() should default to false when mutual auth is disabled")
	}
	if !(ClientOptions{MutualAuth: true}).needInitContinuation() {
		t.Fatal("needInitContinuation() should be true when mutual auth is enabled")
	}
}
