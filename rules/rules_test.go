package rules

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/sylv/tailgate/service"
)

func rule(section, cond string, effects ...Effect) Rule {
	return Rule{ID: cond, Section: section, Name: cond, Condition: cond, Effects: effects, Mode: "on"}
}

func TestCompileErrors(t *testing.T) {
	for _, c := range []struct {
		rule  Rule
		field string
		want  string
	}{
		{rule(Pre, `user.name != "ezra"`, Effect{Type: Block}), "condition", "go in post-auth"},
		{rule(Post, `service.addr != ""`, Effect{Type: Block}), "condition", "no value called addr"},
		{rule(Pre, `path`, Effect{Type: Block}), "condition", "expected bool"},
		{rule(Pre, `true`, Effect{Type: Block}, Effect{Type: Skip}), "effects", "only have one"},
		{rule(Pre, `true`, Effect{Type: Redirect, Value: `"/"`, Status: 200}), "effects.0", "status"},
		{rule(Pre, `true`, Effect{Type: Redirect}), "effects.0", "value is required"},
		{rule(Pre, `true`, Effect{Type: "login"}), "effects.0", "isn't an effect"},
		{rule(Post, `true`, Effect{Type: SetRequestHeader, Name: "x-forwarded-for", Value: `ip`}), "effects.0", "set by tailgate"},
		{rule(Post, `true`, Effect{Type: RemoveRequestHeader, Name: "X-Real-IP"}), "effects.0", "set by tailgate"},
		{rule(Post, `true`, Effect{Type: SetRequestHeader, Name: "X User", Value: `ip`}), "effects.0", "valid header"},
		{rule(Post, `true`, Effect{Type: SetRequestHeader, Name: "X-Port", Value: `service.port`}), "effects.0", "expected string"},
	} {
		_, errs := Compile([]Rule{c.rule})
		if !slices.ContainsFunc(errs, func(e Error) bool { return e.Field == c.field && strings.Contains(e.Message, c.want) }) {
			t.Errorf("%s %v: got %v, want %s %q", c.rule.Condition, c.rule.Effects, errs, c.field, c.want)
		}
	}

	e := Check(Pre, "path == \"/\" &&\n  country + 1", true)
	if e == nil || e.Line != 2 || e.Column != 11 {
		t.Errorf("got %+v, want an error at 2:11", e)
	}
	if e := Check(Post, `"https://" + host + "/new/" + trimPrefix(path, "/old/")`, false); e != nil {
		t.Error(e)
	}
}

func TestRun(t *testing.T) {
	set, errs := Compile([]Rule{
		rule(Pre, `inCidr(ip, "203.0.113.0/24")`, Effect{Type: Skip}, Effect{Type: SetRequestHeader, Name: "X-Home", Value: `"yes"`}),
		rule(Pre, `ingress == "funnel" && country not in ["AU", "CA"]`, Effect{Type: Block, Value: `"Not in " + country`}),
		{ID: "log", Section: Pre, Name: "try", Condition: `path startsWith "/mcp"`, Effects: []Effect{{Type: Block}}, Mode: "log"},
		{ID: "off", Section: Pre, Name: "off", Condition: `true`, Effects: []Effect{{Type: Block}}, Mode: "off"},
		rule(Post, `service.id == "lyra"`, Effect{Type: SetRequestHeader, Name: "X-User", Value: `user.name`}, Effect{Type: SetResponseHeader, Name: "Set-Cookie", Value: `"a=1"`}),
		rule(Post, `user.via == "key"`, Effect{Type: SetRequestHeader, Name: "X-User", Value: `"key:" + user.name`}, Effect{Type: RemoveRequestHeader, Name: "Referer"}),
		rule(Post, `header("X-Old") == "1"`, Effect{Type: Redirect, Value: `"https://" + host + "/new"`, Status: 308}),
	})
	if errs != nil {
		t.Fatal(errs)
	}
	base := Request{Ingress: "funnel", Country: "NZ", IP: "198.51.100.1", Host: "lyra.ts.net", Path: "/mcp", Service: service.Service{ID: "lyra"},
		Header: func(string) string { return "" }}

	var res Result
	if err := set.Run(Pre, base, User{}, &res); err != nil || res.End == nil || res.End.Message != "Not in NZ" {
		t.Fatalf("got %+v %v, want a block", res.End, err)
	}
	if len(res.Logged) != 0 {
		t.Error("a log only rule below the block ran")
	}

	// the home range skips the country block but keeps its own header
	res = Result{}
	home := base
	home.IP = "203.0.113.9"
	if err := set.Run(Pre, home, User{}, &res); err != nil || res.End != nil {
		t.Fatalf("got %+v %v, want no end", res.End, err)
	}
	h := http.Header{}
	res.ApplyRequest(h)
	if h.Get("X-Home") != "yes" {
		t.Errorf("got %v, want the skipping rule's header", h)
	}

	// log only matches but doesn't act
	res = Result{}
	tailnet := base
	tailnet.Ingress = "tailnet"
	if err := set.Run(Pre, tailnet, User{}, &res); err != nil || res.End != nil || !slices.Equal(res.Logged, []string{"try: block"}) {
		t.Errorf("got %+v %v %v", res.End, res.Logged, err)
	}

	// later header effects win, removes apply and Set-Cookie adds
	res = Result{}
	if err := set.Run(Post, tailnet, User{SignedIn: true, Name: "ezra", Via: "key"}, &res); err != nil || res.End != nil {
		t.Fatal(res.End, err)
	}
	h = http.Header{"X-User": {"faked"}, "Referer": {"https://elsewhere"}}
	res.ApplyRequest(h)
	if h.Get("X-User") != "key:ezra" || h.Get("Referer") != "" {
		t.Errorf("got request headers %v", h)
	}
	h = http.Header{"Set-Cookie": {"app=1"}}
	res.ApplyResponse(h)
	if len(h.Values("Set-Cookie")) != 2 {
		t.Errorf("got response headers %v", h)
	}

	res = Result{}
	old := tailnet
	old.Header = func(name string) string { return map[string]string{"X-Old": "1"}[name] }
	set.Run(Post, old, User{SignedIn: true}, &res)
	if res.End == nil || res.End.URL != "https://lyra.ts.net/new" || res.End.Status != 308 {
		t.Errorf("got %+v, want a redirect", res.End)
	}
}

func TestRunError(t *testing.T) {
	// a range that isn't a literal can only fail at runtime
	set, errs := Compile([]Rule{rule(Pre, `inCidr(ip, header("X-Range"))`, Effect{Type: Block})})
	if errs != nil {
		t.Fatal(errs)
	}
	req := Request{IP: "1.2.3.4", Header: func(string) string { return "nope" }}
	if err := set.Run(Pre, req, User{}, &Result{}); err == nil {
		t.Error("a broken cidr didn't fail")
	}
}

func TestSchema(t *testing.T) {
	pre := map[string]bool{}
	for _, v := range Schema() {
		pre[v.Name] = v.Pre
	}
	if !pre["user.signedIn"] || pre["user.name"] || !pre["service.dangerouslyDisableAuth"] || !pre["header"] {
		t.Errorf("got %v", pre)
	}
	if _, ok := pre["service.addr"]; ok {
		t.Error("the container address is a value")
	}
}

func TestBadCidr(t *testing.T) {
	e := Check(Pre, "ip != \"\" &&\n  inCidr(ip, \"10.0.0/8\")", true)
	if e == nil || e.Line != 2 || !strings.Contains(e.Message, "10.0.0/8") {
		t.Fatalf("got %+v", e)
	}
	if e := Check(Pre, `inCidr(ip, "10.0.0.0/8")`, true); e != nil {
		t.Fatalf("got %+v", e)
	}
}
