package toolruntime

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mingzhi1/coden/internal/core/retrieval"
)

// WebFetchTool implements Executor for web page fetching operations.
type WebFetchTool struct {
	client *http.Client
}

// NewWebFetchTool creates a new web fetch tool with a configured HTTP client.
func NewWebFetchTool() *WebFetchTool {
	return &WebFetchTool{
		client: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("too many redirects")
				}
				return nil
			},
		},
	}
}

// Execute implements Executor.
func (t *WebFetchTool) Execute(ctx context.Context, req Request) (Result, error) {
	switch req.Kind {
	case "web_fetch":
		return t.executeFetch(ctx, req)
	default:
		return Result{}, fmt.Errorf("unsupported web fetch tool kind: %s", req.Kind)
	}
}

// executeFetch fetches a URL and returns its content converted to markdown.
func (t *WebFetchTool) executeFetch(ctx context.Context, req Request) (Result, error) {
	urlStr := strings.TrimSpace(req.Path)
	if urlStr == "" {
		urlStr = strings.TrimSpace(req.Content)
	}
	if urlStr == "" {
		return Result{}, fmt.Errorf("web_fetch: url is required")
	}

	// Validate URL
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return Result{}, fmt.Errorf("web_fetch: invalid url: %w", err)
	}
	if parsedURL.Scheme == "" {
		parsedURL.Scheme = "https"
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return Result{}, fmt.Errorf("web_fetch: unsupported scheme: %s", parsedURL.Scheme)
	}

	if err := checkSSRF(parsedURL.Hostname()); err != nil {
		return Result{}, err
	}

	// Create request
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, parsedURL.String(), nil)
	if err != nil {
		return Result{}, fmt.Errorf("web_fetch: failed to create request: %w", err)
	}

	// Set headers to mimic a browser
	httpReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	httpReq.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	httpReq.Header.Set("Accept-Language", "en-US,en;q=0.9")

	// Execute request
	resp, err := t.client.Do(httpReq)
	if err != nil {
		return Result{}, fmt.Errorf("web_fetch: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("web_fetch: HTTP %d", resp.StatusCode)
	}

	// Read body with 10MB limit
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return Result{}, fmt.Errorf("web_fetch: failed to read response: %w", err)
	}

	// Convert to markdown
	contentType := resp.Header.Get("Content-Type")
	markdown := convertToMarkdown(body, contentType, parsedURL.String())

	// Build structured evidence
	evidence := retrieval.RetrievalEvidence{
		Source:      "web_fetch",
		Path:        parsedURL.String(),
		Snippet:     truncateString(markdown, 300),
		Verified:    true,
		Explanation: fmt.Sprintf("Fetched content from %s", parsedURL.String()),
	}

	return Result{
		Summary:        fmt.Sprintf("fetched %d bytes from %s", len(body), parsedURL.String()),
		Output:         markdown,
		StructuredData: []retrieval.RetrievalEvidence{evidence},
	}, nil
}

// checkSSRF resolves the hostname and rejects private/loopback/link-local addresses
// to prevent the LLM from accessing internal network services.
func checkSSRF(hostname string) error {
	addrs, err := net.LookupHost(hostname)
	if err != nil {
		return fmt.Errorf("web_fetch: could not resolve host %q: %w", hostname, err)
	}
	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip == nil {
			continue
		}
		if isPrivateIP(ip) {
			return fmt.Errorf("web_fetch: requests to private/internal addresses are not allowed")
		}
	}
	return nil
}

// isPrivateIP reports whether ip is a loopback, link-local, or private address.
func isPrivateIP(ip net.IP) bool {
	private := []string{
		"127.0.0.0/8",    // loopback
		"::1/128",        // IPv6 loopback
		"10.0.0.0/8",     // RFC-1918
		"172.16.0.0/12",  // RFC-1918
		"192.168.0.0/16", // RFC-1918
		"169.254.0.0/16", // link-local
		"fe80::/10",      // IPv6 link-local
		"fc00::/7",       // IPv6 unique local
		"100.64.0.0/10",  // CGNAT (RFC-6598)
		"0.0.0.0/8",      // "this" network
	}
	for _, cidr := range private {
		_, block, _ := net.ParseCIDR(cidr)
		if block.Contains(ip) {
			return true
		}
	}
	return false
}

// convertToMarkdown converts HTML content to markdown-like format.
func convertToMarkdown(body []byte, contentType, urlStr string) string {
	// Check if it's HTML
	if !strings.Contains(contentType, "text/html") && !strings.Contains(contentType, "application/xhtml") {
		// Not HTML, return as plain text
		return string(body)
	}

	html := string(body)

	// Remove script and style tags with their content
	html = removeTag(html, "script")
	html = removeTag(html, "style")
	html = removeTag(html, "nav")
	html = removeTag(html, "header")
	html = removeTag(html, "footer")
	html = removeTag(html, "aside")

	// Convert common tags to markdown
	html = strings.ReplaceAll(html, "<h1>", "# ")
	html = strings.ReplaceAll(html, "</h1>", "\n\n")
	html = strings.ReplaceAll(html, "<h2>", "## ")
	html = strings.ReplaceAll(html, "</h2>", "\n\n")
	html = strings.ReplaceAll(html, "<h3>", "### ")
	html = strings.ReplaceAll(html, "</h3>", "\n\n")
	html = strings.ReplaceAll(html, "<h4>", "#### ")
	html = strings.ReplaceAll(html, "</h4>", "\n\n")
	html = strings.ReplaceAll(html, "<h5>", "##### ")
	html = strings.ReplaceAll(html, "</h5>", "\n\n")
	html = strings.ReplaceAll(html, "<h6>", "###### ")
	html = strings.ReplaceAll(html, "</h6>", "\n\n")

	html = strings.ReplaceAll(html, "<p>", "\n")
	html = strings.ReplaceAll(html, "</p>", "\n")
	html = strings.ReplaceAll(html, "<br>", "\n")
	html = strings.ReplaceAll(html, "<br/>", "\n")
	html = strings.ReplaceAll(html, "<br />", "\n")

	// Lists
	html = strings.ReplaceAll(html, "<ul>", "\n")
	html = strings.ReplaceAll(html, "</ul>", "\n")
	html = strings.ReplaceAll(html, "<ol>", "\n")
	html = strings.ReplaceAll(html, "</ol>", "\n")
	html = strings.ReplaceAll(html, "<li>", "- ")
	html = strings.ReplaceAll(html, "</li>", "\n")

	// Code blocks
	html = strings.ReplaceAll(html, "<pre>", "```\n")
	html = strings.ReplaceAll(html, "</pre>", "\n```\n")
	html = strings.ReplaceAll(html, "<code>", "`")
	html = strings.ReplaceAll(html, "</code>", "`")

	// Links
	html = replaceLinks(html)

	// Bold and italic
	html = strings.ReplaceAll(html, "<strong>", "**")
	html = strings.ReplaceAll(html, "</strong>", "**")
	html = strings.ReplaceAll(html, "<b>", "**")
	html = strings.ReplaceAll(html, "</b>", "**")
	html = strings.ReplaceAll(html, "<em>", "*")
	html = strings.ReplaceAll(html, "</em>", "*")
	html = strings.ReplaceAll(html, "<i>", "*")
	html = strings.ReplaceAll(html, "</i>", "*")

	// Remove remaining HTML tags
	html = stripTags(html)

	// Clean up whitespace
	html = cleanWhitespace(html)

	return html
}

// removeTag removes HTML tag and its content.
func removeTag(html, tag string) string {
	openTag := "<" + tag
	closeTag := "</" + tag + ">"

	for {
		start := strings.Index(strings.ToLower(html), openTag)
		if start == -1 {
			break
		}

		// Find end of opening tag
		endOpen := strings.Index(html[start:], ">")
		if endOpen == -1 {
			break
		}
		endOpen += start + 1

		// Find closing tag
		end := strings.Index(strings.ToLower(html[start:]), closeTag)
		if end == -1 {
			// Self-closing or no closing tag, just remove the opening tag
			html = html[:start] + html[endOpen:]
			continue
		}
		end += start + len(closeTag)

		html = html[:start] + html[end:]
	}

	return html
}

// stripTags removes all remaining HTML tags.
func stripTags(html string) string {
	var result strings.Builder
	inTag := false

	for _, r := range html {
		switch r {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag {
				result.WriteRune(r)
			}
		}
	}

	return result.String()
}

// replaceLinks converts anchor tags to markdown links.
func replaceLinks(html string) string {
	// Simple regex-free link conversion
	for {
		start := strings.Index(strings.ToLower(html), "<a")
		if start == -1 {
			break
		}

		// Find href
		hrefStart := strings.Index(strings.ToLower(html[start:]), "href=")
		if hrefStart == -1 {
			// No href, just remove the tag
			endTag := strings.Index(html[start:], ">")
			if endTag == -1 {
				break
			}
			html = html[:start] + html[start+endTag+1:]
			continue
		}
		hrefStart += start + 5

		// Get href value
		var hrefEnd int
		var quoteChar byte
		if html[hrefStart] == '"' || html[hrefStart] == '\'' {
			quoteChar = html[hrefStart]
			hrefStart++
			hrefEnd = strings.Index(html[hrefStart:], string(quoteChar))
		} else {
			hrefEnd = strings.IndexAny(html[hrefStart:], " >")
		}
		if hrefEnd == -1 {
			break
		}
		href := html[hrefStart : hrefStart+hrefEnd]

		// Find end of opening tag
		endOpen := strings.Index(html[start:], ">")
		if endOpen == -1 {
			break
		}
		endOpen += start + 1

		// Find closing tag
		closeStart := strings.Index(strings.ToLower(html[endOpen:]), "</a>")
		if closeStart == -1 {
			// No closing tag, just remove the opening tag
			html = html[:start] + html[endOpen:]
			continue
		}
		closeStart += endOpen

		// Get link text
		linkText := html[endOpen:closeStart]

		// Replace with markdown
		markdown := fmt.Sprintf("[%s](%s)", strings.TrimSpace(linkText), href)
		html = html[:start] + markdown + html[closeStart+4:]
	}

	return html
}

// cleanWhitespace normalizes whitespace in text.
func cleanWhitespace(text string) string {
	// Replace multiple newlines with double newline
	for strings.Contains(text, "\n\n\n") {
		text = strings.ReplaceAll(text, "\n\n\n", "\n\n")
	}

	// Replace multiple spaces with single space
	for strings.Contains(text, "  ") {
		text = strings.ReplaceAll(text, "  ", " ")
	}

	return strings.TrimSpace(text)
}
