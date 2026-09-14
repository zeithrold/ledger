package config

import "testing"

func FuzzGinMode(f *testing.F) {
	for _, seed := range []string{"debug", "release", "test", "", "DEBUG", "test\x00"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, mode string) {
		wantValid := mode == "debug" || mode == "release" || mode == "test"
		if (validateGinMode(mode) == nil) != wantValid {
			t.Fatalf("unexpected validation for %q", mode)
		}
	})
}
