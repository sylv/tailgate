// Package rules compiles and runs the admin's rules. Conditions and effect
// values are expr expressions checked against the request values at save time
package rules

import (
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/file"
	"github.com/expr-lang/expr/vm"
	"golang.org/x/net/http/httpguts"

	"github.com/sylv/tailgate/app"
)

const (
	Pre  = "pre"
	Post = "post"
)

const (
	Block                = "block"
	Redirect             = "redirect"
	Skip                 = "skip"
	SetRequestHeader     = "set-request-header"
	RemoveRequestHeader  = "remove-request-header"
	SetResponseHeader    = "set-response-header"
	RemoveResponseHeader = "remove-response-header"
)

type Rule struct {
	ID        string   `json:"id"`
	Section   string   `json:"section"`
	Name      string   `json:"name"`
	Condition string   `json:"condition"`
	Effects   []Effect `json:"effects"`
	// Mode is on, log or off
	Mode string `json:"mode"`
}

type Effect struct {
	Type string `json:"type"`
	// Name is the header name
	Name string `json:"name,omitempty"`
	// Value is the message, URL or header value, as an expression
	Value  string `json:"value,omitempty"`
	Status int    `json:"status,omitempty"`
}

// Request is every value a rule sees except user
type Request struct {
	Ingress   string  `expr:"ingress" desc:"\"tailnet\" or \"funnel\""`
	App       app.App `expr:"app"`
	Host      string  `expr:"host" desc:"The request's host, like photos.your-tailnet.ts.net"`
	Method    string  `expr:"method" desc:"\"GET\", \"POST\" and so on"`
	Path      string  `expr:"path" desc:"The path without the query, like /mcp"`
	Query     string  `expr:"query" desc:"The raw query string without the ?"`
	IP        string  `expr:"ip" desc:"The client IP, the same one sent in X-Real-IP"`
	Country   string  `expr:"country" desc:"ISO 3166 code like \"AU\", or \"\" for tailnet requests and unknown IPs"`
	UserAgent string  `expr:"userAgent" desc:"The User-Agent header"`
	Tailnet   Tailnet `expr:"tailnet"`

	Header func(name string) string `expr:"header" desc:"The request header's value, \"\" when missing. Names are case-insensitive"`
	Cookie func(name string) string `expr:"cookie" desc:"The request cookie's value, \"\" when missing"`
	Param  func(name string) string `expr:"param" desc:"The first query parameter with that name, \"\" when missing"`
}

type Tailnet struct {
	Login string   `expr:"login" desc:"The Tailscale login of the connecting device's owner, like \"sylv@\". \"\" for Funnel, tagged devices, subnet routers and exit nodes, which speak for more than one person"`
	Node  string   `expr:"node" desc:"The connecting device's name, \"\" for Funnel"`
	Tags  []string `expr:"tags" desc:"The connecting device's tags, empty for Funnel"`
}

type User struct {
	SignedIn  bool   `expr:"signedIn" desc:"The request carries a valid tailgate credential"`
	ID        string `expr:"id" desc:"The user's ID, \"\" when not signed in"`
	Name      string `expr:"name" desc:"The user's name, \"\" when not signed in"`
	Email     string `expr:"email" desc:"The user's email, \"\" when unset"`
	Admin     bool   `expr:"admin" desc:"The user is an admin"`
	HasAccess bool   `expr:"hasAccess" desc:"The user's app list includes this app"`
	Via       string `expr:"via" desc:"\"session\", \"key\", \"tailnet\" or \"\""`
}

// preUser is all pre-auth rules get, since a condition on who the user is
// would also match every signed out visitor
type preUser struct {
	SignedIn bool `expr:"signedIn"`
}

type env[U any] struct {
	Request
	User U `expr:"user"`
}

var inCidr = expr.Function("inCidr", func(p ...any) (any, error) {
	prefix, err := netip.ParsePrefix(p[1].(string))
	if err != nil {
		return nil, err
	}
	ip, err := netip.ParseAddr(p[0].(string))
	return err == nil && prefix.Contains(ip.Unmap()), nil
}, new(func(string, string) bool))

func compile(section, src string, asBool bool) (*vm.Program, error) {
	opts := []expr.Option{inCidr, expr.AsKind(reflect.String)}
	if asBool {
		opts[1] = expr.AsBool()
	}
	if section == Pre {
		opts = append(opts, expr.Env(env[preUser]{}))
	} else {
		opts = append(opts, expr.Env(env[User]{}))
	}
	prog, err := expr.Compile(src, opts...)
	if err != nil {
		return nil, err
	}
	var bad cidrCheck
	node := prog.Node()
	ast.Walk(&node, &bad)
	if bad.err != nil {
		return nil, bad.err.Bind(prog.Source())
	}
	return prog, nil
}

// cidrCheck finds a range in inCidr that can't be parsed, which would otherwise
// only fail when a request runs the rule
type cidrCheck struct{ err *file.Error }

func (c *cidrCheck) Visit(node *ast.Node) {
	call, ok := (*node).(*ast.CallNode)
	if !ok || c.err != nil || len(call.Arguments) != 2 {
		return
	}
	if id, ok := call.Callee.(*ast.IdentifierNode); !ok || id.Value != "inCidr" {
		return
	}
	if s, ok := call.Arguments[1].(*ast.StringNode); ok {
		if _, err := netip.ParsePrefix(s.Value); err != nil {
			c.err = &file.Error{Location: s.Location(), Message: fmt.Sprintf("%q isn't an IP range like \"10.0.0.0/8\"", s.Value)}
		}
	}
}

// Error is a problem with one field of one rule. Field is name, condition,
// effects or effects.N, and Line and Column are 1-based when known
type Error struct {
	Rule    int    `json:"rule"`
	Field   string `json:"field"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
	Message string `json:"message"`
}

func (e Error) Error() string { return fmt.Sprintf("rule %d %s: %s", e.Rule+1, e.Field, e.Message) }

var noField = regexp.MustCompile(`^type [\w.]+ has no field (\w+)$`)

func exprError(err error) Error {
	var fe *file.Error
	if !errors.As(err, &fe) {
		return Error{Message: err.Error()}
	}
	msg := fe.Message
	// expr names our go types, which mean nothing to whoever writes the rule
	if strings.HasPrefix(msg, "type rules.preUser ") {
		msg = "Pre-auth rules can only use user.signedIn, so rules about who the user is go in post-auth"
	} else if m := noField.FindStringSubmatch(msg); m != nil {
		msg = "There's no value called " + m[1]
	}
	return Error{Line: fe.Line, Column: fe.Column + 1, Message: msg}
}

// Check compiles one expression for the editor, as a bool for a condition or a string for a value
func Check(section, src string, asBool bool) *Error {
	if _, err := compile(section, src, asBool); err != nil {
		e := exprError(err)
		return &e
	}
	return nil
}

type Set struct {
	sections map[string][]*compiled
}

type compiled struct {
	Rule
	cond *vm.Program
	// values has a program for each effect, nil when it has no value
	values []*vm.Program
}

var protected = []string{"X-Tailgate-Request-Id", "X-Real-Ip"}

// Compile checks every rule and returns all the problems it finds
func Compile(rules []Rule) (*Set, []Error) {
	s := &Set{sections: map[string][]*compiled{}}
	var errs []Error
	for i, r := range rules {
		fail := func(field string, e Error) {
			e.Rule, e.Field = i, field
			errs = append(errs, e)
		}
		if r.Section != Pre && r.Section != Post {
			fail("section", Error{Message: "The section must be pre-auth or post-auth"})
			continue
		}
		if !slices.Contains([]string{"on", "log", "off"}, r.Mode) {
			fail("mode", Error{Message: "The mode must be on, log only or off"})
		}
		if strings.TrimSpace(r.Name) == "" {
			fail("name", Error{Message: "A name is required"})
		}
		c := &compiled{Rule: r}
		var err error
		if strings.TrimSpace(r.Condition) == "" {
			fail("condition", Error{Message: "A condition is required"})
		} else if c.cond, err = compile(r.Section, r.Condition, true); err != nil {
			fail("condition", exprError(err))
		}
		if len(r.Effects) == 0 {
			fail("effects", Error{Message: "A rule needs at least one effect"})
		}
		ending := 0
		for j, e := range r.Effects {
			field := fmt.Sprintf("effects.%d", j)
			var prog *vm.Program
			switch e.Type {
			case Block, Redirect, Skip:
				ending++
			case SetRequestHeader, RemoveRequestHeader, SetResponseHeader, RemoveResponseHeader:
				name := http.CanonicalHeaderKey(e.Name)
				switch {
				case !httpguts.ValidHeaderFieldName(e.Name):
					fail(field, Error{Message: "That isn't a valid header name"})
				case slices.Contains(protected, name) || strings.HasPrefix(name, "X-Forwarded-"):
					fail(field, Error{Message: name + " is set by tailgate and can't be changed by rules"})
				}
			default:
				fail(field, Error{Message: fmt.Sprintf("%q isn't an effect", e.Type)})
				continue
			}
			if e.Type == Redirect && !slices.Contains([]int{0, 301, 302, 303, 307, 308}, e.Status) {
				fail(field, Error{Message: "A redirect's status must be 301, 302, 303, 307 or 308"})
			}
			needsValue := e.Type == Redirect || e.Type == SetRequestHeader || e.Type == SetResponseHeader
			if needsValue && strings.TrimSpace(e.Value) == "" {
				fail(field, Error{Message: "A value is required"})
			} else if (needsValue || e.Type == Block) && strings.TrimSpace(e.Value) != "" {
				if prog, err = compile(r.Section, e.Value, false); err != nil {
					fail(field, exprError(err))
				}
			}
			c.values = append(c.values, prog)
		}
		if ending > 1 {
			fail("effects", Error{Message: "A rule can only have one of block, redirect or skip"})
		}
		s.sections[r.Section] = append(s.sections[r.Section], c)
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return s, nil
}

// Result collects what the rules decided as they run
type Result struct {
	// End is set by the block or redirect that ends the request
	End     *End
	headers []header
	// Logged has what each matching log only rule would have done
	Logged []string
}

type End struct {
	Rule string
	// URL is "" for a block
	URL     string
	Status  int
	Message string
}

type header struct {
	response, remove bool
	name, value      string
}

// ApplyRequest changes the request to the app
func (res *Result) ApplyRequest(h http.Header) {
	for _, op := range res.headers {
		switch {
		case op.response:
		case op.remove:
			h.Del(op.name)
		default:
			h.Set(op.name, op.value)
		}
	}
}

// ApplyResponse changes the app's response. Set-Cookie is added next to the app's cookies
func (res *Result) ApplyResponse(h http.Header) {
	for _, op := range res.headers {
		switch {
		case !op.response:
		case op.remove:
			h.Del(op.name)
		case http.CanonicalHeaderKey(op.name) == "Set-Cookie":
			h.Add(op.name, op.value)
		default:
			h.Set(op.name, op.value)
		}
	}
}

// Run runs one section's rules until one ends the request or skips the rest
func (s *Set) Run(section string, req Request, u User, res *Result) error {
	var e any = env[User]{req, u}
	if section == Pre {
		e = env[preUser]{req, preUser{u.SignedIn}}
	}
	for _, c := range s.sections[section] {
		if c.Mode == "off" {
			continue
		}
		match, err := expr.Run(c.cond, e)
		if err != nil {
			return fmt.Errorf("rule %q: %w", c.Name, err)
		}
		if !match.(bool) {
			continue
		}
		values := make([]string, len(c.values))
		for i, p := range c.values {
			if p == nil {
				continue
			}
			v, err := expr.Run(p, e)
			if err != nil {
				return fmt.Errorf("rule %q: %w", c.Name, err)
			}
			values[i] = v.(string)
		}
		if c.Mode == "log" {
			var did []string
			for _, ef := range c.Effects {
				did = append(did, ef.Type)
			}
			res.Logged = append(res.Logged, c.Name+": "+strings.Join(did, ", "))
			continue
		}
		skip := false
		for i, ef := range c.Effects {
			switch ef.Type {
			case Block:
				res.End = &End{Rule: c.Name, Status: http.StatusForbidden, Message: values[i]}
			case Redirect:
				res.End = &End{Rule: c.Name, URL: values[i], Status: ef.Status}
				if res.End.Status == 0 {
					res.End.Status = http.StatusFound
				}
			case Skip:
				skip = true
			default:
				res.headers = append(res.headers, header{
					response: ef.Type == SetResponseHeader || ef.Type == RemoveResponseHeader,
					remove:   ef.Type == RemoveRequestHeader || ef.Type == RemoveResponseHeader,
					name:     ef.Name,
					value:    values[i],
				})
			}
		}
		if res.End != nil || skip {
			return nil
		}
	}
	return nil
}

// Value is one name the editor completes, with its type and description
type Value struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Desc string `json:"desc"`
	// Pre is whether pre-auth rules can use it
	Pre bool `json:"pre"`
}

var builtins = []Value{
	{"inCidr", "func(ip string, cidr string) bool", `Whether the IP is in the range, like inCidr(ip, "10.0.0.0/8")`, true},
	{"lower", "func(string) string", "The string in lowercase", true},
	{"upper", "func(string) string", "The string in uppercase", true},
	{"trim", "func(string) string", "The string without leading and trailing spaces", true},
	{"trimPrefix", "func(string, prefix string) string", "The string without the prefix", true},
	{"trimSuffix", "func(string, suffix string) string", "The string without the suffix", true},
	{"split", "func(string, sep string) []string", "The parts of the string between each sep", true},
	{"len", "func(any) int", "The length of a string or list", true},
	{"any", "func(list, predicate) bool", "Whether any item matches, like any(tailnet.tags, # == \"tag:server\")", true},
	{"all", "func(list, predicate) bool", "Whether every item matches", true},
	{"none", "func(list, predicate) bool", "Whether no item matches", true},
	{"now", "func() time.Time", "The current time in the container's TZ, like now().Hour() >= 22", true},
}

// Schema lists every value and function a rule can use
func Schema() []Value {
	var values []Value
	pre := map[string]bool{}
	var walk func(prefix string, t reflect.Type, add func(name string, f reflect.StructField))
	walk = func(prefix string, t reflect.Type, add func(string, reflect.StructField)) {
		for i := range t.NumField() {
			f := t.Field(i)
			name := prefix + f.Tag.Get("expr")
			switch {
			case f.Anonymous:
				walk(prefix, f.Type, add)
			case f.Tag.Get("expr") == "-":
			case f.Type.Kind() == reflect.Struct:
				walk(name+".", f.Type, add)
			default:
				add(name, f)
			}
		}
	}
	walk("", reflect.TypeFor[env[preUser]](), func(name string, _ reflect.StructField) { pre[name] = true })
	walk("", reflect.TypeFor[env[User]](), func(name string, f reflect.StructField) {
		values = append(values, Value{name, f.Type.String(), f.Tag.Get("desc"), pre[name]})
	})
	return append(values, builtins...)
}
