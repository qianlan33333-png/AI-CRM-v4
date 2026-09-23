package port

import "testing"

func TestSafePublicCompletionURLRejectsLegacyIPv4Forms(t *testing.T) {
	for _, raw := range []string{
		"https://2130706433/complete",
		"https://127.1/complete",
		"https://0177.0.0.1/complete",
		"https://0x7f000001/complete",
		"https://0300.0250.0001.0001/complete",
	} {
		if SafePublicCompletionURL(raw) {
			t.Fatalf("legacy IPv4 completion URL accepted: %q", raw)
		}
	}
}

func TestSafePublicCompletionURLRejectsLocalhostNames(t *testing.T) {
	for _, raw := range []string{
		"https://localhost/complete",
		"https://foo.localhost/complete",
		"https://receiver.LOCALHOST./complete",
	} {
		if SafePublicCompletionURL(raw) {
			t.Fatalf("localhost completion URL accepted: %q", raw)
		}
	}
}

func TestSafePublicCompletionURLAcceptsSupportedDestinations(t *testing.T) {
	for _, raw := range []string{"/same-origin/complete", "https://example.com/complete", "https://subdomain.127.example/complete"} {
		if !SafePublicCompletionURL(raw) {
			t.Fatalf("supported completion URL rejected: %q", raw)
		}
	}
}

func TestValidPublicCompletionURLNormalizesUnicodeHostsBeforePublicCheck(t *testing.T) {
	for _, test := range []struct {
		name string
		url  string
		want bool
	}{
		{name: "full width loopback", url: "https://１２７.０.０.１/finished"},
		{name: "ideographic full stops", url: "https://127。0。0。1/finished"},
		{name: "full width full stops", url: "https://127．0．0．1/finished"},
		{name: "half width ideographic full stops", url: "https://127｡0｡0｡1/finished"},
		{name: "full width octal", url: "https://０１７７.０.０.１/finished"},
		{name: "full width hexadecimal", url: "https://０x７ｆ０００００１/finished"},
		{name: "full width mixed radix", url: "https://１２７.０x０.１/finished"},
		{name: "international public domain", url: "https://例え.テスト/finished", want: true},
		{name: "width mapped public domain", url: "https://ｅxample.com/finished", want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := ValidPublicCompletionURL(test.url); got != test.want {
				t.Fatalf("ValidPublicCompletionURL(%q)=%t want %t", test.url, got, test.want)
			}
		})
	}
}
