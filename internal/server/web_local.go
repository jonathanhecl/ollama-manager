package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// Browser-like UA: DuckDuckGo Lite and many sites return empty or challenge pages to generic/“bot” UAs.
const webClientUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36 (ollama-manager)"
const maxFetchBody = 2 << 20 // 2 MiB

var reSpace = regexp.MustCompile(`\s+`)

// isBlockedIP returns true for loopback, private, link-local, and other non-public targets (SSRF mitigation).
func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		// 0.0.0.0/8, 127.0.0.0/8 already loopback; block CGNAT 100.64.0.0/10, 169.254.0.0/16 link-local, 192.0.0.0/24, etc.
		if ip4[0] == 0 || ip4[0] == 10 || (ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127) || (ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31) ||
			(ip4[0] == 169 && ip4[1] == 254) || (ip4[0] == 192 && ip4[1] == 0 && ip4[2] == 0) || (ip4[0] == 192 && ip4[1] == 88 && ip4[2] == 99) {
			return true
		}
	}
	if len(ip) == net.IPv6len {
		// unique local (fc00::/7) — simplified check on first byte
		if ip[0] == 0xfc || ip[0] == 0xfd {
			return true
		}
	}
	return false
}

// assertPublicURL resolves the host and ensures no IP in the result set is loopback/private (best-effort SSRF guard).
func assertPublicURL(ctx context.Context, u *url.URL) error {
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("missing host")
	}
	if net.ParseIP(host) != nil {
		ip := net.ParseIP(host)
		if isBlockedIP(ip) {
			return fmt.Errorf("address not allowed")
		}
		return nil
	}
	r := net.Resolver{}
	addrs, err := r.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("dns: %w", err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("no addresses for host")
	}
	for _, a := range addrs {
		if isBlockedIP(a.IP) {
			return fmt.Errorf("host resolves to a non-public address")
		}
	}
	return nil
}

func newWebHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 25 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 8 {
				return fmt.Errorf("too many redirects")
			}
			return assertPublicURL(req.Context(), req.URL)
		},
	}
}

func getAttr(n *html.Node, key string) string {
	if n == nil {
		return ""
	}
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// htmlNodeText appends visible text (skips script/style/noscript) from a subtree.
func htmlNodeText(n *html.Node, sb *strings.Builder) {
	if n == nil {
		return
	}
	if n.Type == html.TextNode {
		sb.WriteString(n.Data)
		return
	}
	if n.Type != html.ElementNode {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			htmlNodeText(c, sb)
		}
		return
	}
	switch strings.ToLower(n.Data) {
	case "script", "style", "noscript", "template", "svg", "canvas":
		return
	}
	if c := n.FirstChild; c == nil {
		return
	} else {
		for ; c != nil; c = c.NextSibling {
			htmlNodeText(c, sb)
		}
	}
}

func firstTagText(doc *html.Node, tag string) string {
	var walk func(*html.Node) string
	walk = func(n *html.Node) string {
		if n == nil {
			return ""
		}
		if n.Type == html.ElementNode && strings.EqualFold(n.Data, tag) {
			var sb strings.Builder
			htmlNodeText(n, &sb)
			if sb.Len() > 0 {
				return strings.TrimSpace(sb.String())
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if s := walk(c); s != "" {
				return s
			}
		}
		return ""
	}
	return walk(doc)
}

// extractVisibleText parses HTML and returns a normalized plain string.
func extractVisibleText(htmlBytes []byte) (title string, body string) {
	doc, err := html.Parse(bytes.NewReader(htmlBytes))
	if err != nil {
		return "", string(htmlBytes)
	}
	title = firstTagText(doc, "title")
	var bodySB strings.Builder
	if bodyNode := findFirst(doc, "body"); bodyNode != nil {
		htmlNodeText(bodyNode, &bodySB)
	} else {
		htmlNodeText(doc, &bodySB)
	}
	body = strings.TrimSpace(reSpace.ReplaceAllString(bodySB.String(), " "))
	if utf8.RuneCountInString(body) > 80000 {
		body = truncateRunes(body, 80000)
	}
	return title, body
}

func findFirst(n *html.Node, tag string) *html.Node {
	if n == nil {
		return nil
	}
	if n.Type == html.ElementNode && strings.EqualFold(n.Data, tag) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if f := findFirst(c, tag); f != nil {
			return f
		}
	}
	return nil
}

func collectLinks(doc *html.Node, max int) []string {
	var out []string
	seen := make(map[string]struct{})
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n == nil || len(out) >= max {
			return
		}
		if n.Type == html.ElementNode && strings.EqualFold(n.Data, "a") {
			h := getAttr(n, "href")
			if h != "" {
				if u, err := url.Parse(h); err == nil {
					if u.Scheme == "http" || u.Scheme == "https" {
						abs := u.String()
						if _, ok := seen[abs]; !ok {
							seen[abs] = struct{}{}
							out = append(out, abs)
						}
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return out
}

// localWebFetch downloads a page over HTTP(s) and returns title + main text + some links. No Ollama Cloud.
func (s *Server) localWebFetch(ctx context.Context, pageURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(pageURL))
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("only http/https URLs are allowed")
	}
	if err := assertPublicURL(ctx, u); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", webClientUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	client := newWebHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<10))
		msg := strings.TrimSpace(string(b))
		if len(msg) > 600 {
			msg = msg[:600] + "…"
		}
		if msg == "" {
			msg = "(empty body)"
		}
		return "", fmt.Errorf("http %d: %s", resp.StatusCode, msg)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBody))
	if err != nil {
		return "", err
	}
	title, text := extractVisibleText(raw)
	links := ""
	if d, err := html.Parse(bytes.NewReader(raw)); err == nil {
		lnk := collectLinks(d, 18)
		if len(lnk) > 0 {
			links = "\n\nExtracted links:\n"
			for _, l := range lnk {
				links += "- " + l + "\n"
			}
		}
	}
	var sb strings.Builder
	if title != "" {
		fmt.Fprintf(&sb, "Title: %s\n\n", title)
	}
	sb.WriteString(text)
	if links != "" {
		sb.WriteString(links)
	}
	return sb.String(), nil
}

// ----- DuckDuckGo (no API key) -----

// unwrapDDGResultURL normalizes result links from DuckDuckGo Lite. They are often
// protocol-relative (//duckduckgo.com/...) and use /l/?uddg=<encoded target URL>.
func unwrapDDGResultURL(href string) (string, bool) {
	href = strings.TrimSpace(href)
	if href == "" {
		return "", false
	}
	if strings.HasPrefix(href, "//") {
		href = "https:" + href
	}
	u, err := url.Parse(href)
	if err != nil {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	duck := host == "duckduckgo.com" || strings.HasSuffix(host, ".duckduckgo.com")
	if duck && strings.HasPrefix(u.Path, "/l") {
		uddg := u.Query().Get("uddg")
		if uddg != "" {
			dec, err := url.QueryUnescape(uddg)
			if err == nil && (strings.HasPrefix(dec, "http://") || strings.HasPrefix(dec, "https://")) {
				return dec, true
			}
		}
	}
	if u.Scheme == "http" || u.Scheme == "https" {
		// Use parsed form for consistent string (drops fragments, etc. where applicable).
		if duck && strings.Contains(u.Path, "duckduckgo-help") {
			return "", false
		}
		return u.String(), true
	}
	return "", false
}

type ddgAPIResponse struct {
	AbstractText  string         `json:"AbstractText"`
	AbstractURL   string         `json:"AbstractURL"`
	Abstract      string         `json:"Abstract"`
	RelatedTopics []ddgRelTopic  `json:"RelatedTopics"`
	Results       []ddgJSONEntry `json:"Results"`
}

type ddgRelTopic struct {
	Text     string        `json:"Text"`
	FirstURL string        `json:"FirstURL"`
	Topics   []ddgRelTopic `json:"Topics"`
}

type ddgJSONEntry struct {
	Text string `json:"Text"`
	URL  string `json:"FirstURL"`
}

func flattenDDGTopics(t []ddgRelTopic, out *[]ddgJSONEntry) {
	for _, r := range t {
		if r.Text != "" && r.FirstURL != "" {
			*out = append(*out, ddgJSONEntry{Text: r.Text, URL: r.FirstURL})
		}
		if len(r.Topics) > 0 {
			flattenDDGTopics(r.Topics, out)
		}
	}
}

func (s *Server) ddgInstantSearch(ctx context.Context, query string) string {
	u := "https://api.duckduckgo.com/?q=" + url.QueryEscape(query) + "&format=json&no_html=1&skip_disambig=1"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("User-Agent", webClientUA)
	client := newWebHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ""
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
	var d ddgAPIResponse
	if err := json.Unmarshal(b, &d); err != nil {
		return ""
	}
	var sb strings.Builder
	if d.AbstractText != "" {
		if d.AbstractURL != "" {
			fmt.Fprintf(&sb, "Summary: %s\nSource: %s\n\n", strings.TrimSpace(d.AbstractText), d.AbstractURL)
		} else {
			fmt.Fprintf(&sb, "Summary: %s\n\n", strings.TrimSpace(d.AbstractText))
		}
	} else if d.Abstract != "" {
		fmt.Fprintf(&sb, "Summary: %s\n\n", strings.TrimSpace(d.Abstract))
	}
	var rel []ddgJSONEntry
	flattenDDGTopics(d.RelatedTopics, &rel)
	rel = append(rel, d.Results...)
	for i, e := range rel {
		if i >= 5 {
			break
		}
		if e.Text == "" {
			continue
		}
		su := e.URL
		if su != "" {
			fmt.Fprintf(&sb, "• %s\n  %s\n", strings.TrimSpace(e.Text), su)
		} else {
			fmt.Fprintf(&sb, "• %s\n", strings.TrimSpace(e.Text))
		}
	}
	return sb.String()
}

// ddgLiteScrape fetches HTML search results from DuckDuckGo Lite.
func (s *Server) ddgLiteScrape(ctx context.Context, query string, max int) (string, error) {
	if max <= 0 {
		max = 5
	}
	if max > 10 {
		max = 10
	}
	u := "https://lite.duckduckgo.com/lite/?q=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", webClientUA)
	req.Header.Set("Accept", "text/html")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9,es;q=0.8")
	client := newWebHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return "", fmt.Errorf("search http %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", err
	}
	doc, err := html.Parse(bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	var hits [][2]string
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n == nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == "a" {
			cls := getAttr(n, "class")
			if strings.Contains(cls, "result-link") {
				href := getAttr(n, "href")
				if final, ok := unwrapDDGResultURL(href); ok {
					var t strings.Builder
					htmlNodeText(n, &t)
					title := strings.TrimSpace(t.String())
					if title == "" {
						title = final
					}
					if !strings.EqualFold(title, "more info") {
						hits = append(hits, [2]string{title, final})
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(doc)
	seen := make(map[string]struct{})
	var sb strings.Builder
	n := 0
	for _, h := range hits {
		if n >= max {
			break
		}
		u := h[1]
		if _, ok := seen[u]; ok {
			continue
		}
		seen[u] = struct{}{}
		n++
		fmt.Fprintf(&sb, "%d. %s\n%s\n", n, h[0], u)
	}
	if sb.Len() == 0 {
		return "", fmt.Errorf("no HTML results (layout may have changed)")
	}
	return sb.String(), nil
}

// localWebSearch uses DuckDuckGo JSON instant answers plus Lite HTML. No Ollama Cloud, no API key.
func (s *Server) localWebSearch(ctx context.Context, query string, maxResults int) (string, error) {
	if maxResults <= 0 {
		maxResults = 5
	}
	if maxResults > 10 {
		maxResults = 10
	}
	inst := s.ddgInstantSearch(ctx, query)
	liteN := maxResults
	if inst != "" {
		liteN = max(1, maxResults-2)
	}
	lite, errLite := s.ddgLiteScrape(ctx, query, liteN)

	var parts []string
	if inst != "" {
		parts = append(parts, "--- Instant answer / related (DuckDuckGo) ---\n"+strings.TrimSpace(inst))
	}
	if errLite == nil && lite != "" {
		if inst != "" {
			parts = append(parts, "--- Search results (DuckDuckGo Lite) ---\n"+strings.TrimSpace(lite))
		} else {
			parts = append(parts, strings.TrimSpace(lite))
		}
	} else if inst == "" {
		if errLite != nil {
			return "", fmt.Errorf("web search: %v", errLite)
		}
		return "No search results (try a different phrasing).", nil
	}
	if len(parts) == 0 {
		return "No search results.", nil
	}
	if errLite != nil && inst != "" {
		parts = append(parts, fmt.Sprintf("(Note: full result list was unavailable: %v)", errLite))
	}
	return strings.Join(parts, "\n\n"), nil
}
