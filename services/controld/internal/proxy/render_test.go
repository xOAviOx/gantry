package proxy

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "update golden files")

func basicInput() RenderInput {
	return RenderInput{
		DashboardHost: "paas.localhost",
		AppsSuffix:    "apps.localhost",
		HostInternal:  "host.docker.internal",
		ControldPort:  8080,
		WebPort:       3000,
		AdminListen:   "0.0.0.0:2019",
		AdminOrigins:  []string{"127.0.0.1:2019", "localhost:2019"},
		Apps: []AppUpstream{
			{Slug: "hello", Dial: "gantry-hello-abcd1234:3000"},
			{Slug: "blog", Dial: "gantry-blog-9f8e7d6c:8000"},
		},
	}
}

// TestRenderGolden pins the rendered Caddy JSON to a golden file. Run with
// `go test ./... -run Golden -update` to regenerate after intentional changes.
func TestRenderGolden(t *testing.T) {
	got, err := Render(basicInput())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	golden := filepath.Join("testdata", "render_basic.json")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with -update first): %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("rendered config != golden\n--- got ---\n%s", got)
	}
}

// TestRenderStructure asserts the invariants the DoD depends on, independent of
// exact formatting.
func TestRenderStructure(t *testing.T) {
	blob, err := Render(basicInput())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	var cfg struct {
		Admin struct {
			Listen string `json:"listen"`
		} `json:"admin"`
		Apps struct {
			HTTP struct {
				Servers map[string]struct {
					Listen []string `json:"listen"`
					Routes []struct {
						Match []struct {
							Host []string `json:"host"`
						} `json:"match"`
					} `json:"routes"`
				} `json:"servers"`
			} `json:"http"`
		} `json:"apps"`
	}
	if err := json.Unmarshal(blob, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if cfg.Admin.Listen != "0.0.0.0:2019" {
		t.Errorf("admin.listen = %q, want 0.0.0.0:2019 (must survive every /load)", cfg.Admin.Listen)
	}
	srv, ok := cfg.Apps.HTTP.Servers["gantry"]
	if !ok {
		t.Fatal(`missing server "gantry"`)
	}
	if len(srv.Listen) != 1 || srv.Listen[0] != ":80" {
		t.Errorf("server listen = %v, want [:80]", srv.Listen)
	}

	// dashboard + 2 apps = 3 routes; verify the app hosts are present.
	hosts := map[string]bool{}
	for _, r := range srv.Routes {
		for _, m := range r.Match {
			for _, h := range m.Host {
				hosts[h] = true
			}
		}
	}
	for _, want := range []string{"paas.localhost", "hello.apps.localhost", "blog.apps.localhost"} {
		if !hosts[want] {
			t.Errorf("missing host route %q (have %v)", want, hosts)
		}
	}
}
