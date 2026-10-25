// Command sserecord records raw provider SSE response bodies to
// pkg/agentcore/llm/testdata/sse for replay tests (see README.md there).
//
// It sends, per model, one plain-text request and a two-step tool-call
// conversation, dumping the raw SSE byte stream of each response next to the
// request body it answered. Only the response bodies and request bodies are
// saved: no HTTP headers, so no Authorization header or API key ever reaches a
// fixture. The conversation content is fixed test text without user data.
//
// Configuration is read (read-only) from an existing SUGA_HOME, so provider
// base_url, model_id and the ${VAR} key references resolve exactly as in
// production; the key values come from that home's .env and are kept in
// memory only. Flags select the models and the output directory.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
)

type options struct {
	configFrom string // SUGA_HOME to read config.yaml and .env from
	out        string // output directory for the fixtures
	models     string // comma separated provider/model pairs, empty = all
	timeout    time.Duration
}

type providerConf struct {
	Type    string `yaml:"type"`
	BaseURL string `yaml:"base_url"`
	APIKey  string `yaml:"api_key"`
	Models  map[string]struct {
		ModelID string `yaml:"model_id"`
		Type    string `yaml:"type"` // per-model override, e.g. anthropic
	} `yaml:"models"`
}

type config struct {
	Providers map[string]providerConf `yaml:"providers"`
}

// target is one model to record: the command line's "<provider>/<model>" plus
// the resolved upstream model id and protocol (chat | messages | responses).
type target struct {
	provider, model, modelID, proto, baseURL, apiKey string
	// noThinking drops the thinking parameter after the gateway rejected it
	// (some models of a thinking-capable gateway do not support it).
	noThinking bool
}

func main() {
	var o options
	flag.StringVar(&o.configFrom, "config-from", "", "SUGA_HOME to read config.yaml and .env from (default $SUGA_HOME or ~/.suga)")
	flag.StringVar(&o.out, "out", filepath.Join("pkg", "agentcore", "llm", "testdata", "sse"), "output directory")
	flag.StringVar(&o.models, "models", "", "comma separated provider/model pairs to record (default: all)")
	flag.DurationVar(&o.timeout, "timeout", 120*time.Second, "per-request timeout")
	flag.Parse()

	if err := run(o); err != nil {
		fmt.Fprintln(os.Stderr, "sserecord:", err)
		os.Exit(1)
	}
}

func run(o options) error {
	cfg, env, err := loadConfig(o.configFrom)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(o.out, 0o755); err != nil {
		return err
	}
	targets, err := pickTargets(cfg, env, o.models)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, t := range targets {
		if err := recordTarget(ctx, o, t); err != nil {
			// One failing model must not stop the rest of the report.
			fmt.Fprintf(os.Stderr, "sserecord: %s/%s: %v\n", t.provider, t.model, err)
		}
	}
	return nil
}

// loadConfig reads the SUGA_HOME's config.yaml (read-only) and its .env into a
// name→value map used to expand ${VAR} references. Nothing is written back.
func loadConfig(home string) (*config, map[string]string, error) {
	if home == "" {
		home = os.Getenv("SUGA_HOME")
	}
	if home == "" {
		home = filepath.Join(os.Getenv("USERPROFILE"), ".suga")
		if home == filepath.Join("", ".suga") {
			home = filepath.Join(os.Getenv("HOME"), ".suga")
		}
	}
	data, err := os.ReadFile(filepath.Join(home, "config.yaml"))
	if err != nil {
		return nil, nil, fmt.Errorf("read config.yaml: %w", err)
	}
	cfg := &config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, nil, fmt.Errorf("parse config.yaml: %w", err)
	}
	env := map[string]string{}
	if envData, err := os.ReadFile(filepath.Join(home, ".env")); err == nil {
		for _, line := range strings.Split(string(envData), "\n") {
			line = strings.TrimSpace(line)
			if i := strings.Index(line, "="); i > 0 {
				env[strings.TrimSpace(line[:i])] = strings.TrimSpace(line[i+1:])
			}
		}
	}
	resolveRefs(cfg, env)
	return cfg, env, nil
}

// resolveRefs expands ${VAR} api_key references against the .env values. The
// map values are addressed by key because ranging gives a copy.
func resolveRefs(cfg *config, env map[string]string) {
	for id, p := range cfg.Providers {
		p.APIKey = os.Expand(p.APIKey, func(k string) string { return env[k] })
		cfg.Providers[id] = p
	}
}

func pickTargets(cfg *config, env map[string]string, models string) ([]target, error) {
	var out []target
	for _, pair := range strings.Split(models, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		providerID, model, ok := strings.Cut(pair, "/")
		if !ok {
			return nil, fmt.Errorf("expected provider/model, got %q", pair)
		}
		p, found := cfg.Providers[providerID]
		if !found {
			return nil, fmt.Errorf("provider %q not found in %s", providerID, "config.yaml")
		}
		m, found := p.Models[model]
		if !found {
			return nil, fmt.Errorf("provider %q has no model %q", providerID, model)
		}
		id := m.ModelID
		if id == "" {
			id = model
		}
		proto := m.Type
		if proto == "" {
			switch p.Type {
			case "anthropic":
				proto = "messages"
			case "openai-responses":
				proto = "responses"
			default:
				proto = "chat"
			}
		} else if proto == "anthropic" {
			proto = "messages"
		}
		out = append(out, target{
			provider: providerID, model: model, modelID: id, proto: proto,
			baseURL: strings.TrimSuffix(p.BaseURL, "/"), apiKey: p.APIKey,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no models to record (use --models provider/model)")
	}
	return out, nil
}

// recordTarget records three fixtures for one model: a plain-text answer and
// the two steps of a tool-call conversation.
func recordTarget(ctx context.Context, o options, t target) error {
	fmt.Printf("== %s/%s (%s) %s\n", t.provider, t.model, t.proto, t.baseURL)
	_, err := recordCase(ctx, o, t, "text", nil, "")
	if err != nil && t.proto == "messages" && strings.Contains(err.Error(), "thinking") {
		// The gateway does not support thinking for this model: record without it.
		fmt.Printf("   retrying without thinking\n")
		t.noThinking = true
		_, err = recordCase(ctx, o, t, "text", nil, "")
	}
	if err != nil {
		return err
	}
	call, err := recordCase(ctx, o, t, "tool1", nil, "")
	if err != nil {
		return err
	}
	if call.name == "" {
		return fmt.Errorf("no tool call in the tool1 response")
	}
	if _, err := recordCase(ctx, o, t, "tool2", &call, "list_files"); err != nil {
		return err
	}
	return nil
}

// toolCall is the one call the tool1 step is expected to make.
type toolCall struct {
	id   string
	name string
	args string // raw JSON of the streamed arguments
}

// recordCase builds the request for one case, saves its body as
// <provider>-<model>-<case>.json and the raw response as .sse, and returns the
// tool call the response asked for (tool1 only).
func recordCase(ctx context.Context, o options, t target, c string, prev *toolCall, toolName string) (toolCall, error) {
	body, err := buildBody(t, c, prev)
	if err != nil {
		return toolCall{}, err
	}
	path := filepath.Join(o.out, fmt.Sprintf("%s-%s-%s.sse", t.provider, t.model, c))
	reqPath := strings.TrimSuffix(path, ".sse") + ".json"
	reqData, _ := json.MarshalIndent(body, "", "  ")
	if err := os.WriteFile(reqPath, reqData, 0o600); err != nil {
		return toolCall{}, err
	}
	res, call, err := post(ctx, o, t, body)
	if err != nil {
		os.Remove(reqPath)
		return toolCall{}, err
	}
	if err := os.WriteFile(path, res, 0o644); err != nil {
		return toolCall{}, err
	}
	fmt.Printf("   %-6s %6d bytes  call=%s(%s)\n", c, len(res), call.name, call.args)
	return call, nil
}

// listFilesTool is the simple tool every tool-case conversation offers.
const listFilesTool = `{"type":"function","function":{"name":"list_files","description":"List the files of a directory","parameters":{"type":"object","properties":{"directory":{"type":"string","description":"The directory to list"}},"required":["directory"]}}}`

// toolResultText is the fixed fake tool result of the second step.
const toolResultText = "notes.md,\nmain.go,\ngo.mod"

// buildBody constructs the request payload for one case, per protocol. The
// content is fixed test text so no user data reaches a fixture.
func buildBody(t target, c string, prev *toolCall) (map[string]any, error) {
	userMsg := map[string]any{"role": "user", "content": "Call the list_files tool to list the files of the directory named fixtures."}
	textMsg := map[string]any{"role": "user", "content": "Reply with exactly one short sentence and no tool call."}

	switch t.proto {
	case "chat":
		out := map[string]any{"model": t.modelID, "stream": true, "max_tokens": 2048}
		var msgs []map[string]any
		switch c {
		case "text":
			msgs = []map[string]any{textMsg}
		case "tool1":
			out["tools"] = []any{json.RawMessage(listFilesTool)}
			msgs = []map[string]any{userMsg}
		case "tool2":
			out["tools"] = []any{json.RawMessage(listFilesTool)}
			msgs = append(msgs, userMsg, map[string]any{
				"role": "assistant", "content": nil,
				"tool_calls": []any{map[string]any{
					"id": prev.id, "type": "function",
					"function": map[string]any{"name": prev.name, "arguments": prev.args},
				}},
			}, map[string]any{"role": "tool", "tool_call_id": prev.id, "content": toolResultText},
				map[string]any{"role": "user", "content": "Now answer with the file list you received."})
		}
		out["messages"] = msgs
		return out, nil

	case "messages":
		out := map[string]any{"model": t.modelID, "stream": true, "max_tokens": 4096}
		// Ask for thinking so the stream carries thinking_delta and
		// signature_delta events, the parts the replay test must reproduce.
		// Adaptive (not budget) thinking is what the newer models take; haiku
		// 5.5 answers directly unless the prompt asks it to work the answer out
		// in the thinking block first, so the prompts below do that.
		if !t.noThinking {
			out["thinking"] = map[string]any{"type": "adaptive", "display": "summarized"}
			out["output_config"] = map[string]any{"effort": "high"}
		}
		userMsg = map[string]any{"role": "user", "content": "First work out which files to look for in your thinking block, then call the list_files tool to list the files of the directory named fixtures."}
		textMsg = map[string]any{"role": "user", "content": "First think about what makes a good short answer, then reply with exactly one short sentence."}
		var msgs []map[string]any
		switch c {
		case "text":
			msgs = []map[string]any{textMsg}
		case "tool1":
			out["tools"] = []any{map[string]any{
				"name": "list_files", "description": "List the files of a directory",
				"input_schema": map[string]any{"type": "object", "properties": map[string]any{
					"directory": map[string]any{"type": "string", "description": "The directory to list"}},
					"required": []string{"directory"}},
			}}
			msgs = []map[string]any{userMsg}
		case "tool2":
			out["tools"] = []any{map[string]any{
				"name": "list_files", "description": "List the files of a directory",
				"input_schema": map[string]any{"type": "object", "properties": map[string]any{
					"directory": map[string]any{"type": "string", "description": "The directory to list"}},
				},
			}}
			msgs = append(msgs, userMsg, map[string]any{
				"role": "assistant",
				"content": []any{map[string]any{"type": "tool_use", "id": prev.id, "name": prev.name,
					"input": json.RawMessage(prev.args)}},
			}, map[string]any{
				"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": prev.id, "content": toolResultText}},
			})
		}
		out["messages"] = msgs
		out["system"] = "You are a helpful assistant. Keep answers to one or two sentences."
		return out, nil

	case "responses":
		input := func(ms ...map[string]any) []any {
			items := make([]any, 0, len(ms))
			for _, m := range ms {
				role := m["role"].(string)
				if role == "assistant" {
					items = append(items, map[string]any{"type": "function_call", "call_id": prev.id, "name": prev.name, "arguments": prev.args})
					continue
				}
				items = append(items, map[string]any{
					"role": role, "content": []any{map[string]any{"type": "input_text", "text": m["content"]}},
				})
			}
			return items
		}
		out := map[string]any{"model": t.modelID, "stream": true, "max_output_tokens": 2048}
		switch c {
		case "text":
			out["input"] = input(textMsg)
		case "tool1":
			out["tools"] = []any{map[string]any{
				"type": "function", "name": "list_files", "description": "List the files of a directory",
				"parameters": map[string]any{"type": "object", "properties": map[string]any{
					"directory": map[string]any{"type": "string", "description": "The directory to list"}},
					"required": []string{"directory"}},
			}}
			out["input"] = input(userMsg)
		case "tool2":
			out["tools"] = []any{map[string]any{
				"type": "function", "name": "list_files", "description": "List the files of a directory",
				"parameters": map[string]any{"type": "object", "properties": map[string]any{
					"directory": map[string]any{"type": "string", "description": "The directory to list"}},
					"required": []string{"directory"}},
			}}
			items := []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": userMsg["content"]}}}}
			items = append(items, map[string]any{"type": "function_call", "call_id": prev.id, "name": prev.name, "arguments": prev.args})
			items = append(items, map[string]any{"type": "function_call_output", "call_id": prev.id, "output": toolResultText})
			items = append(items, map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": "Now answer with the file list you received."}}})
			out["input"] = items
		}
		return out, nil
	}
	return nil, fmt.Errorf("unknown protocol %q", t.proto)
}

// post sends one request, returns the raw response body bytes and the tool
// call extracted from the stream (naive per-protocol accumulation, only used
// to build the second step's request).
func post(ctx context.Context, o options, t target, body map[string]any) (raw []byte, call toolCall, err error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, call, err
	}
	url := t.baseURL
	switch t.proto {
	case "messages":
		url += "/messages"
	case "chat":
		url += "/chat/completions"
	case "responses":
		url += "/responses"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, call, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	switch t.proto {
	case "messages":
		req.Header.Set("x-api-key", t.apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	default:
		req.Header.Set("Authorization", "Bearer "+t.apiKey)
	}
	client := &http.Client{Timeout: o.timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, call, err
	}
	defer resp.Body.Close()
	raw, err = io.ReadAll(resp.Body)
	if err != nil {
		return raw, call, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return raw, call, fmt.Errorf("HTTP %d: %.200s", resp.StatusCode, raw)
	}
	curl := extractCall(t.proto, raw)
	return raw, curl, err
}

// extractCall pulls the single tool call out of a recorded stream without
// depending on the product's parser (the recorder must stay independent of the
// code under test).
func extractCall(proto string, raw []byte) toolCall {
	var args strings.Builder
	var call toolCall
	eachData := func(line string, fn func(payload []byte)) {
		_, payload, ok := strings.Cut(line, "data:")
		if !ok {
			return
		}
		fn([]byte(payload))
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		switch proto {
		case "chat":
			eachData(line, func(payload []byte) {
				var chunk struct {
					Choices []struct {
						Delta struct {
							ToolCalls []struct {
								ID       string `json:"id"`
								Function struct {
									Name      string `json:"name"`
									Arguments string `json:"arguments"`
								} `json:"function"`
							} `json:"tool_calls"`
						} `json:"delta"`
					} `json:"choices"`
				}
				if json.Unmarshal(payload, &chunk) != nil {
					return
				}
				for _, c := range chunk.Choices {
					for _, tc := range c.Delta.ToolCalls {
						if tc.ID != "" {
							call.id = tc.ID
						}
						if tc.Function.Name != "" {
							call.name = tc.Function.Name
						}
						args.WriteString(tc.Function.Arguments)
					}
				}
			})
		case "messages":
			eachData(line, func(payload []byte) {
				var ev struct {
					Type         string `json:"type"`
					ContentBlock struct {
						Type string `json:"type"`
						ID   string `json:"id"`
						Name string `json:"name"`
					} `json:"content_block"`
					Delta struct {
						Type        string `json:"type"`
						PartialJSON string `json:"partial_json"`
					} `json:"delta"`
				}
				if json.Unmarshal(payload, &ev) != nil {
					return
				}
				switch {
				case ev.Type == "content_block_start" && ev.ContentBlock.Type == "tool_use":
					call.id, call.name = ev.ContentBlock.ID, ev.ContentBlock.Name
				case ev.Type == "content_block_delta" && ev.Delta.Type == "input_json_delta":
					args.WriteString(ev.Delta.PartialJSON)
				}
			})
		case "responses":
			eachData(line, func(payload []byte) {
				var ev struct {
					Type string `json:"type"`
					Item struct {
						ID        string          `json:"id"`
						CallID    string          `json:"call_id"`
						Name      string          `json:"name"`
						Arguments string          `json:"arguments"`
						Raw       json.RawMessage `json:"input"`
					} `json:"item"`
				}
				if json.Unmarshal(payload, &ev) != nil {
					return
				}
				if ev.Type == "response.output_item.done" && ev.Item.CallID != "" && ev.Item.Name != "" {
					call.id, call.name = ev.Item.CallID, ev.Item.Name
					args.Reset()
					args.WriteString(ev.Item.Arguments)
				}
			})
		}
	}
	call.args = args.String()
	if strings.TrimSpace(call.args) == "" {
		call.args = "{}"
	}
	return call
}
