package build

import "testing"

func TestImageTag(t *testing.T) {
	got := ImageTag("hello", "6ef72bc3-2d7e-4f0d-aac2-11e8a5605563")
	want := "gantry/hello:d-6ef72bc3"
	if got != want {
		t.Errorf("ImageTag = %q, want %q", got, want)
	}
}

func TestDeploy8(t *testing.T) {
	cases := map[string]string{
		"6ef72bc3-2d7e-4f0d-aac2-11e8a5605563": "6ef72bc3",
		"abcd":                                 "abcd",
		"":                                     "",
	}
	for in, want := range cases {
		if got := deploy8(in); got != want {
			t.Errorf("deploy8(%q) = %q, want %q", in, got, want)
		}
	}
}
