package deploy

import "testing"

func TestHasAllMarkers(t *testing.T) {
	// A representative live Caddy config with the dashboard host and one app route.
	cfg := []byte(`{"apps":{"http":{"servers":{"gantry":{"listen":[":80"],"routes":[
		{"match":[{"host":["paas.localhost"]}],"handle":[{"handler":"subroute"}]},
		{"match":[{"host":["hello.apps.localhost"]}],"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"gantry-hello-abc123:3000"}]}]}
	]}}}}}`)

	if !hasAllMarkers(cfg, []string{"paas.localhost", "hello.apps.localhost", "gantry-hello-abc123:3000"}) {
		t.Fatal("expected all markers present")
	}
	// A missing route (host or dial) is drift.
	if hasAllMarkers(cfg, []string{"paas.localhost", "missing.apps.localhost"}) {
		t.Fatal("missing host marker should be reported as drift")
	}
	if hasAllMarkers(cfg, []string{"gantry-hello-abc123:3000", "gantry-other:3000"}) {
		t.Fatal("missing dial marker should be reported as drift")
	}
	// A wiped config (Caddy reset) drops everything.
	if hasAllMarkers([]byte("null"), []string{"paas.localhost"}) {
		t.Fatal("wiped config must be reported as drift")
	}
	// No markers (no live apps) trivially in sync.
	if !hasAllMarkers([]byte("{}"), nil) {
		t.Fatal("empty marker set should be in sync")
	}
}
