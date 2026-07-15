package deploy

import "testing"

func TestContainerName(t *testing.T) {
	got := ContainerName("hello", "6ef72bc3-2d7e-4f0d-aac2-11e8a5605563")
	want := "gantry-hello-6ef72bc3"
	if got != want {
		t.Errorf("ContainerName = %q, want %q", got, want)
	}
}

func TestShort(t *testing.T) {
	cases := map[string]string{
		"6ef72bc3-2d7e-4f0d-aac2-11e8a5605563": "6ef72bc3",
		"ab-cd-ef-gh-ij":                       "abcdefgh",
		"xyz":                                  "xyz",
	}
	for in, want := range cases {
		if got := short(in); got != want {
			t.Errorf("short(%q) = %q, want %q", in, got, want)
		}
	}
}
