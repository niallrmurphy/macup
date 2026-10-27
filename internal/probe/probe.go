// Package probe experiments with evidence-based update discovery. It never installs updates.
package probe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/niallrmurphy/macup/internal/inventory"
)

type Candidate struct {
	URL    string `json:"url"`
	Source string `json:"source"`
	Score  int    `json:"score"`
}
type Page struct {
	URL      string   `json:"url"`
	Snippets []string `json:"snippets,omitempty"`
	Error    string   `json:"error,omitempty"`
}
type Interpretation struct {
	Finding  string `json:"finding"`
	Version  string `json:"version,omitempty"`
	Evidence string `json:"evidence,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Verified bool   `json:"verified"`
}

// Management records who is responsible for updating an application when it is
// not the application's own embedded feed: the App Store, a parent browser, a
// vendor-wide updater, or nothing because the bundle is a helper.
type Management struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

// Lookup is a structured, keyed query result (App Store catalogue, Homebrew
// cask index, release feed). Comparison is relative to the installed version.
type Lookup struct {
	Source     string `json:"source"`
	Version    string `json:"version,omitempty"`
	Released   string `json:"released,omitempty"`
	URL        string `json:"url,omitempty"`
	Comparison string `json:"comparison"`
	Note       string `json:"note,omitempty"`
}

// Origin is where macOS recorded the application was downloaded from.
type Origin struct {
	URL      string `json:"url"`
	Referrer string `json:"referrer,omitempty"`
	Agent    string `json:"agent,omitempty"`
	Time     string `json:"time,omitempty"`
	Source   string `json:"source"`
}

type Report struct {
	Application inventory.Application `json:"application"`
	Signer      string                `json:"signer,omitempty"`
	TeamID      string                `json:"team_id,omitempty"`
	Families    []string              `json:"families,omitempty"`
	Management  *Management           `json:"management,omitempty"`
	Candidates  []Candidate           `json:"candidates"`
	Origins     []Origin              `json:"origins,omitempty"`
	Lookups     []Lookup              `json:"lookups,omitempty"`
	Pages       []Page                `json:"pages,omitempty"`
	LLM         *Interpretation       `json:"llm,omitempty"`
	Warnings    []string              `json:"warnings,omitempty"`
}
type Options struct {
	Search   string
	Fetch    bool
	Lookup   bool
	Origins  bool
	Model    string
	MaxPages int
}

var urlPattern = regexp.MustCompile(`https://[^\s\x00-\x1f<>"'\\]+`)
var versionPattern = regexp.MustCompile(`(?i)(version|release|download|changelog|what.s new|update|build|latest|\bv\d+\.\d+)`)

// runCommand is indirected so tests can stub external tools.
var runCommand = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// Discover reads scalar plist fields and at most 2 MiB of textual bundle metadata.
// It does not recursively inspect every embedded framework or execute bundle programs.
func Discover(path string) (Report, error) {
	r, _, err := discover(path)
	return r, err
}

func discover(path string) (Report, map[string]string, error) {
	app, err := inventory.ReadBundle(path)
	if err != nil {
		return Report{}, nil, err
	}
	r := Report{Application: app, Candidates: []Candidate{}}
	fields, err := readPlistScalars(bundleFile(path, "Contents", "Info.plist"))
	if err != nil {
		return r, nil, err
	}
	for k, v := range fields {
		for _, u := range urlPattern.FindAllString(v, -1) {
			score := 30
			lower := strings.ToLower(k)
			if strings.Contains(lower, "feed") || strings.Contains(lower, "update") {
				score = 90
			} else if strings.Contains(lower, "url") || strings.Contains(lower, "website") {
				score = 65
			}
			add(&r, u, "Info.plist:"+k, score)
		}
	}
	// A vendor's main website can be useful even if it is not a dedicated update URL.
	// Do not synthesize domains from bundle IDs: those frequently differ from product websites.
	sortCandidates(&r)
	return r, fields, nil
}

func sortCandidates(r *Report) {
	sort.SliceStable(r.Candidates, func(i, j int) bool { return r.Candidates[i].Score > r.Candidates[j].Score })
}

func add(r *Report, raw, source string, score int) {
	u, err := url.Parse(strings.TrimRight(raw, ",.;)"))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return
	}
	for i, c := range r.Candidates {
		if c.URL == u.String() {
			if score > c.Score {
				r.Candidates[i].Score = score
				r.Candidates[i].Source = source
			}
			return
		}
	}
	r.Candidates = append(r.Candidates, Candidate{u.String(), source, score})
}
func (r *Report) warn(format string, args ...any) {
	r.Warnings = append(r.Warnings, fmt.Sprintf(format, args...))
}
func (r *Report) hasFamily(name string) bool {
	for _, f := range r.Families {
		if f == name {
			return true
		}
	}
	return false
}
func (r *Report) addFamily(name string) {
	if !r.hasFamily(name) {
		r.Families = append(r.Families, name)
	}
}
func (r *Report) strongest() int {
	best := 0
	for _, c := range r.Candidates {
		if c.Score > best {
			best = c.Score
		}
	}
	return best
}

func publicIP(ip net.IP) bool {
	return ip != nil && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsUnspecified() && !ip.IsMulticast() && !ip.IsInterfaceLocalMulticast() && !ip.Equal(net.IPv4bcast)
}
func publicHost(ctx context.Context, host string) error {
	if strings.EqualFold(host, "localhost") || !strings.Contains(host, ".") {
		return errors.New("local hostname blocked")
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return err
	}
	if len(ips) == 0 {
		return errors.New("no resolved IP")
	}
	for _, ip := range ips {
		if !publicIP(ip.IP) {
			return errors.New("non-public address blocked")
		}
	}
	return nil
}
func safeClient() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	tr := &http.Transport{Proxy: nil, DisableKeepAlives: true, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		if strings.EqualFold(host, "localhost") || !strings.Contains(host, ".") {
			return nil, errors.New("local hostname blocked")
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, errors.New("no resolved IP")
		}
		for _, ip := range ips {
			if !publicIP(ip.IP) {
				return nil, errors.New("non-public address blocked")
			}
		}
		// Pin the resolved public address; do not perform a second DNS lookup in DialContext.
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}}
	return &http.Client{Timeout: 20 * time.Second, Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// fetchBytes retrieves a bounded public HTTPS resource without following redirects.
var fetchBytes = func(ctx context.Context, raw, accept string, max int) ([]byte, string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return nil, "", errors.New("only HTTPS public URLs allowed")
	}
	if err = publicHost(ctx, u.Hostname()); err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "macup-probe/0.1")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	res, err := safeClient().Do(req)
	if err != nil {
		return nil, "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("HTTP %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, int64(max)+1))
	if err != nil {
		return nil, "", err
	}
	if len(b) > max {
		return nil, "", fmt.Errorf("response exceeds %d bytes", max)
	}
	return b, strings.ToLower(res.Header.Get("Content-Type")), nil
}

func Fetch(ctx context.Context, raw string) (Page, error) {
	p := Page{URL: raw}
	b, ct, err := fetchBytes(ctx, raw, "", 512*1024)
	if err != nil {
		return p, err
	}
	if !strings.Contains(ct, "html") && !strings.Contains(ct, "text/plain") {
		return p, fmt.Errorf("unsupported content type %q", ct)
	}
	text := htmlText(string(b))
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 8 || !versionPattern.MatchString(line) {
			continue
		}
		if len(line) > 350 {
			line = line[:350]
		}
		p.Snippets = append(p.Snippets, line)
		if len(p.Snippets) >= 30 {
			break
		}
	}
	return p, nil
}

var tags = regexp.MustCompile(`(?s)<[^>]+>`)
var blocks = regexp.MustCompile(`(?is)<(script|style|svg|noscript)[^>]*>.*?</(script|style|svg|noscript)>`)
var spaces = regexp.MustCompile(`[ \t\r]+`)

func htmlText(s string) string {
	s = blocks.ReplaceAllString(s, " ")
	s = strings.ReplaceAll(s, "</", "\n</")
	s = tags.ReplaceAllString(s, " ")
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	s = strings.ReplaceAll(s, "&amp;", "&")
	return spaces.ReplaceAllString(s, " ")
}

// needsSearch decides whether the last-resort web search is justified: only
// when nothing local, derived, or keyed has produced a usable update source.
func needsSearch(r *Report) bool {
	if r.strongest() >= 80 {
		return false
	}
	for _, l := range r.Lookups {
		if l.Version != "" {
			return false
		}
	}
	if r.Management != nil {
		switch r.Management.Kind {
		case "appstore", "pwa", "helper", "setapp":
			return false
		}
	}
	return true
}

// Run evaluates evidence in order of cost and trust: bundle contents, local OS
// records, keyed public lookups, and only then web search, page fetches and a
// local model. Search and the model are opt-in and run last.
func Run(ctx context.Context, path string, opt Options) (Report, error) {
	r, fields, err := discover(path)
	if err != nil {
		return r, err
	}
	if opt.MaxPages <= 0 || opt.MaxPages > 3 {
		opt.MaxPages = 3
	}
	inspectBundle(ctx, &r, fields)
	inspectLocal(ctx, &r, fields)
	classify(&r, fields)
	if opt.Origins {
		inspectOrigins(ctx, &r)
	}
	if opt.Lookup {
		lookups(ctx, &r)
	}
	if opt.Search != "" {
		if opt.Search != "brave" {
			return r, fmt.Errorf("unsupported search backend %q", opt.Search)
		}
		if needsSearch(&r) {
			if e := SearchBrave(ctx, &r); e != nil {
				r.warn("search (brave): %s", e.Error())
			}
		}
	}
	sortCandidates(&r)
	if opt.Fetch {
		for i, c := range r.Candidates {
			if i >= opt.MaxPages {
				break
			}
			p, e := Fetch(ctx, c.URL)
			if e != nil {
				p.Error = e.Error()
			}
			r.Pages = append(r.Pages, p)
		}
	}
	if opt.Model != "" {
		if !opt.Fetch {
			return r, errors.New("--model requires --fetch")
		}
		v, e := Ollama(ctx, opt.Model, r)
		if e != nil {
			r.warn("ollama: %s", e.Error())
		} else {
			r.LLM = v
		}
	}
	return r, nil
}

func bundleFile(path string, parts ...string) string {
	return strings.Join(append([]string{path}, parts...), string(os.PathSeparator))
}
