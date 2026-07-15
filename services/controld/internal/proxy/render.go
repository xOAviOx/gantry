// Package proxy renders the entire desired Caddy config from DB state and pushes
// it via the admin API. Rendering is a pure function with golden tests; loading
// is a full POST /load, never a surgical patch (SPEC.md §9, D3).
package proxy

import (
	"encoding/json"
	"strconv"
)

// AppUpstream is one live app route: <slug>.<suffix> -> Dial.
type AppUpstream struct {
	Slug string
	Dial string // "<container>:<port>", resolved via Docker DNS on gantry-apps
}

// RenderInput is everything the pure renderer needs. No hidden globals.
type RenderInput struct {
	DashboardHost string // "paas.localhost"
	AppsSuffix    string // "apps.localhost"
	HostInternal  string // "host.docker.internal"
	ControldPort  int
	WebPort       int
	AdminListen   string   // "0.0.0.0:2019" — MUST be re-declared on every /load
	AdminOrigins  []string // else Caddy resets admin to loopback-only defaults
	Apps          []AppUpstream
}

// Render produces the Caddy native-JSON config as indented bytes.
func Render(in RenderInput) ([]byte, error) {
	return json.MarshalIndent(build(in), "", "  ")
}

func build(in RenderInput) caddyConfig {
	// Dashboard host: /api/* and /webhooks/* -> controld, everything else -> web.
	dashboard := route{
		Match: []match{{Host: []string{in.DashboardHost}}},
		Handle: []any{subroute{
			Handler: "subroute",
			Routes: []route{
				{
					Match: []match{{Path: []string{"/api/*", "/webhooks/*"}}},
					Handle: []any{reverseProxy{
						Handler:   "reverse_proxy",
						Upstreams: []upstream{{Dial: hostPort(in.HostInternal, in.ControldPort)}},
					}},
				},
				{
					Handle: []any{reverseProxy{
						Handler:   "reverse_proxy",
						Upstreams: []upstream{{Dial: hostPort(in.HostInternal, in.WebPort)}},
					}},
				},
			},
		}},
	}

	routes := make([]route, 0, len(in.Apps)+1)
	routes = append(routes, dashboard)
	for _, a := range in.Apps {
		routes = append(routes, route{
			Match: []match{{Host: []string{a.Slug + "." + in.AppsSuffix}}},
			Handle: []any{reverseProxy{
				Handler:   "reverse_proxy",
				Upstreams: []upstream{{Dial: a.Dial}},
			}},
		})
	}

	return caddyConfig{
		Admin:   &admin{Listen: in.AdminListen, Origins: in.AdminOrigins},
		Logging: &logging{Logs: map[string]logCfg{"default": {Level: "INFO"}}},
		Apps: apps{HTTP: httpApp{Servers: map[string]server{
			"gantry": {Listen: []string{":80"}, Routes: routes},
		}}},
	}
}

func hostPort(host string, port int) string {
	return host + ":" + strconv.Itoa(port)
}

// --- Typed Caddy native JSON config (typed for deterministic golden output) ---

type caddyConfig struct {
	Admin   *admin   `json:"admin,omitempty"`
	Logging *logging `json:"logging,omitempty"`
	Apps    apps     `json:"apps"`
}

type admin struct {
	Listen  string   `json:"listen"`
	Origins []string `json:"origins,omitempty"`
}

type logging struct {
	Logs map[string]logCfg `json:"logs"`
}

type logCfg struct {
	Level string `json:"level"`
}

type apps struct {
	HTTP httpApp `json:"http"`
}

type httpApp struct {
	Servers map[string]server `json:"servers"`
}

type server struct {
	Listen []string `json:"listen"`
	Routes []route  `json:"routes"`
}

type route struct {
	Match  []match `json:"match,omitempty"`
	Handle []any   `json:"handle"`
}

type match struct {
	Host []string `json:"host,omitempty"`
	Path []string `json:"path,omitempty"`
}

type reverseProxy struct {
	Handler   string     `json:"handler"`
	Upstreams []upstream `json:"upstreams"`
}

type upstream struct {
	Dial string `json:"dial"`
}

type subroute struct {
	Handler string  `json:"handler"`
	Routes  []route `json:"routes"`
}
