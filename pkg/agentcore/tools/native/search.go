package native

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/netproxy"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// serpAPIEndpoint is the SerpAPI search URL.
const serpAPIEndpoint = "https://serpapi.com/search"

// WebSearchTool performs web searches via SerpAPI (serpapi_key). It only
// reads, so it is registered in read-only mode too.
type WebSearchTool struct {
	endpoint string // SerpAPI URL; tests point it at httptest
	apiKey   string
	client   *http.Client
	maxOut   int // raw result cap in bytes; 0 = DefaultRawOutputBytes
}

// NewWebSearchTool creates a WebSearchTool with the given SerpAPI key. Its
// client follows the environment proxies unless a network proxy is set (see
// SetNetworkProxy).
func NewWebSearchTool(apiKey string) *WebSearchTool {
	return &WebSearchTool{
		endpoint: serpAPIEndpoint,
		apiKey:   apiKey,
		client:   &http.Client{Timeout: 15 * time.Second},
	}
}

// SetNetworkProxy routes web_search through the global network.proxy selector:
// a matched host uses the proxy, every other host stays direct. A nil selector
// (no global proxy configured) keeps the default client, which reads
// HTTP_PROXY/HTTPS_PROXY/NO_PROXY.
func (t *WebSearchTool) SetNetworkProxy(selector *netproxy.Selector) {
	if selector == nil {
		return
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = selector.ProxyFunc()
	t.client = &http.Client{Transport: transport, Timeout: t.client.Timeout}
}

func (t *WebSearchTool) Name() string { return "web_search" }
func (t *WebSearchTool) Description() string {
	return "Search the web using SerpAPI. Returns top organic results."
}

// ToolMeta marks the tool as a read-only search.
func (*WebSearchTool) ToolMeta() tools.Meta {
	return tools.Meta{Category: tools.CategorySearch, ReadOnly: true, External: true}
}
func (t *WebSearchTool) Schema() api.ToolSchema {
	return api.ToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Search query",
				},
				"num": map[string]any{
					"type":        "integer",
					"description": "Number of results (default 5, max 20)",
				},
			},
			"required": []any{"query"},
		},
	}
}

func (t *WebSearchTool) Execute(ctx context.Context, call api.ToolCall) (*api.ToolResult, error) {
	query, _ := call.Arguments["query"].(string)
	if query == "" {
		return nil, fmt.Errorf("web_search: query is required")
	}
	// num is an integer; older callers sent a string, which numberArg accepts.
	num := 5
	if n, ok := numberArg(call.Arguments["num"]); ok && n >= 1 {
		num = min(int(n), 20)
	}

	params := url.Values{
		"api_key": {t.apiKey},
		"q":       {query},
		"num":     {strconv.Itoa(num)},
		"engine":  {"google"},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.endpoint+"?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("web_search build request: %w", err)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("web_search request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("web_search read response: %w", err)
	}

	content, err := t.formatResults(body)
	if err != nil {
		return nil, err
	}
	return &api.ToolResult{
		CallID:  call.CallID,
		Name:    t.Name(),
		Content: TruncateHeadTail(content, rawLimit(t.maxOut)),
	}, nil
}

func (t *WebSearchTool) formatResults(data []byte) (string, error) {
	var result struct {
		OrganicResults []struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Snippet string `json:"snippet"`
		} `json:"organic_results"`
		Error string `json:"error"`
	}

	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("web_search parse: %w", err)
	}
	if result.Error != "" {
		return "", fmt.Errorf("serpapi error: %s", result.Error)
	}

	var sb strings.Builder
	for i, r := range result.OrganicResults {
		fmt.Fprintf(&sb, "%d. %s\n   %s\n   %s\n\n", i+1, r.Title, r.Link, r.Snippet)
	}
	return strings.TrimSpace(sb.String()), nil
}
