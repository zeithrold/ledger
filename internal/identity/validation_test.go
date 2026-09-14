package identity

import "testing"

func TestBootstrapValidation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		in    BootstrapInput
		valid bool
	}{
		{"CNY", BootstrapInput{"CNY", "Asia/Shanghai", "zh-CN"}, true},
		{"USD", BootstrapInput{"USD", "America/New_York", "en-US"}, true},
		{"empty", BootstrapInput{}, false},
		{"unknown currency", BootstrapInput{"ZZZ", "UTC", "en"}, false},
		{"lowercase", BootstrapInput{"usd", "UTC", "en"}, false},
		{"host timezone", BootstrapInput{"USD", "Local", "en"}, false},
		{"bad timezone", BootstrapInput{"USD", "Nowhere/Invalid", "en"}, false},
		{"bad locale", BootstrapInput{"USD", "UTC", "bad!locale"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.in.validate()
			if (err == nil) != tc.valid {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
