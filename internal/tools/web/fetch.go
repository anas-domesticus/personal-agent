package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/cloudwego/eino/components/tool"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"

	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
)

const (
	// MaxFetchBytes is the largest response body web_fetch accepts.
	MaxFetchBytes = 5 << 20
	// UserAgent is sent with every fetch.
	UserAgent = "personal-agent/0.1"

	defaultFetchTimeout = 30
	maxFetchTimeout     = 120
)

type fetchIn struct {
	URL     string `json:"url" jsonschema_description:"The http or https URL to fetch."`
	Format  string `json:"format,omitempty" jsonschema_description:"How to return the content: markdown (default), text, or html."`
	Timeout int    `json:"timeout,omitempty" jsonschema_description:"Timeout in seconds, default 30, at most 120."`
}

type fetchOut struct {
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
	Content     string `json:"content"`
}

const fetchDescription = `Fetch a URL and return its content. HTML pages are converted to markdown by default; use format "text" for plain visible text or "html" for the raw page. Other text content (JSON, plain text, XML) is returned as-is; binary content such as images and PDFs is refused.

Only http and https URLs are supported, responses over 5MB are refused, and long results are truncated.`

func newFetchTool(client *http.Client) (tool.InvokableTool, error) {
	return kit.New("web_fetch", fetchDescription, func(ctx context.Context, in fetchIn) (fetchOut, error) {
		return fetch(ctx, client, in)
	})
}

func fetch(ctx context.Context, client *http.Client, in fetchIn) (fetchOut, error) {
	format := strings.ToLower(strings.TrimSpace(in.Format))
	switch format {
	case "":
		format = "markdown"
	case "markdown", "text", "html":
	default:
		return fetchOut{}, fmt.Errorf("format must be markdown, text or html, not %q", in.Format)
	}
	u, err := url.Parse(strings.TrimSpace(in.URL))
	if err != nil {
		return fetchOut{}, fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fetchOut{}, errors.New("url must start with http:// or https://")
	}
	if u.Host == "" {
		return fetchOut{}, errors.New("url has no host")
	}

	timeout := in.Timeout
	if timeout <= 0 {
		timeout = defaultFetchTimeout
	}
	timeout = min(timeout, maxFetchTimeout)
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fetchOut{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", acceptHeader(format))
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fetchOut{}, fmt.Errorf("fetch timed out after %ds", timeout)
		}
		return fetchOut{}, fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fetchOut{}, fmt.Errorf("request failed with status %s", resp.Status)
	}
	if resp.ContentLength > MaxFetchBytes {
		return fetchOut{}, fmt.Errorf("response too large (%d bytes, limit 5MB)", resp.ContentLength)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxFetchBytes+1))
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fetchOut{}, fmt.Errorf("fetch timed out after %ds", timeout)
		}
		return fetchOut{}, fmt.Errorf("reading response: %w", err)
	}
	if len(body) > MaxFetchBytes {
		return fetchOut{}, errors.New("response too large (limit 5MB)")
	}

	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = http.DetectContentType(body)
	}
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		mt = strings.ToLower(strings.TrimSpace(strings.Split(ct, ";")[0]))
	}
	isHTML := mt == "text/html" || mt == "application/xhtml+xml"
	if !isHTML && !isText(mt) {
		return fetchOut{}, fmt.Errorf("content type %q is binary and can't be returned as text", mt)
	}

	text, err := decode(body, ct)
	if err != nil {
		return fetchOut{}, err
	}
	out := fetchOut{URL: resp.Request.URL.String(), ContentType: ct, Content: text}
	if !isHTML {
		return out, nil
	}
	switch format {
	case "markdown":
		md, err := htmltomarkdown.ConvertString(text, converter.WithDomain(baseURL(resp.Request.URL)))
		if err != nil {
			return fetchOut{}, fmt.Errorf("converting to markdown: %w", err)
		}
		out.Content = md
	case "text":
		t, err := visibleText(text)
		if err != nil {
			return fetchOut{}, err
		}
		out.Content = t
	}
	return out, nil
}

func acceptHeader(format string) string {
	switch format {
	case "markdown":
		return "text/markdown;q=1.0, text/x-markdown;q=0.9, text/plain;q=0.8, text/html;q=0.7, */*;q=0.1"
	case "text":
		return "text/plain;q=1.0, text/markdown;q=0.9, text/html;q=0.8, */*;q=0.1"
	default:
		return "text/html;q=1.0, application/xhtml+xml;q=0.9, text/plain;q=0.8, */*;q=0.1"
	}
}

func baseURL(u *url.URL) string {
	return (&url.URL{Scheme: u.Scheme, Host: u.Host}).String()
}

// isText reports whether a media type is text that can be returned as-is.
func isText(mt string) bool {
	if strings.HasPrefix(mt, "text/") ||
		strings.HasSuffix(mt, "+json") || strings.HasSuffix(mt, "+xml") {
		return true
	}
	switch mt {
	case "application/json", "application/xml", "application/javascript",
		"application/x-javascript", "application/ecmascript", "application/x-yaml",
		"application/yaml", "application/toml", "application/x-ndjson",
		"application/ld+json", "application/x-sh", "application/sql", "image/svg+xml":
		return true
	}
	return false
}

// decode converts body to UTF-8 using the charset in the content type or
// the document, falling back to the bytes as they are.
func decode(body []byte, contentType string) (string, error) {
	r, err := charset.NewReader(bytes.NewReader(body), contentType)
	if err == nil {
		b, err := io.ReadAll(r)
		if err != nil {
			return "", fmt.Errorf("decoding response: %w", err)
		}
		return string(b), nil
	}
	// An unknown charset isn't fatal: return the bytes as they are.
	return string(body), nil
}

var skipText = map[string]bool{
	"script": true, "style": true, "noscript": true, "template": true,
	"iframe": true, "object": true, "embed": true, "svg": true, "head": true,
}

var blockElems = map[string]bool{
	"p": true, "div": true, "br": true, "li": true, "tr": true, "h1": true,
	"h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "section": true,
	"article": true, "header": true, "footer": true, "nav": true, "aside": true,
	"main": true, "ul": true, "ol": true, "table": true, "blockquote": true,
	"pre": true, "hr": true, "dt": true, "dd": true, "figcaption": true, "title": true,
}

var (
	spaces     = regexp.MustCompile(`[ \t\r\f\v\x{00a0}]+`)
	blankLines = regexp.MustCompile(`\n\s*\n+`)
)

// visibleText returns the text a reader would see, skipping scripts, styles
// and other non-content elements.
func visibleText(doc string) (string, error) {
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		return "", fmt.Errorf("parsing html: %w", err)
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			b.WriteString(n.Data)
			return
		case html.ElementNode:
			if skipText[n.Data] {
				return
			}
		case html.CommentNode:
			return
		}
		block := n.Type == html.ElementNode && blockElems[n.Data]
		if block {
			b.WriteByte('\n')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if block {
			b.WriteByte('\n')
		}
	}
	walk(root)

	lines := strings.Split(b.String(), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(spaces.ReplaceAllString(l, " "))
	}
	out := blankLines.ReplaceAllString(strings.Join(lines, "\n"), "\n\n")
	return strings.TrimSpace(out), nil
}
