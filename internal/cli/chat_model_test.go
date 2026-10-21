package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
)

func TestChatModelCommandThroughCLIAndResume(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	t.Setenv("SUGA_HOME", shortTestHome(t))
	workspace := t.TempDir()
	t.Chdir(workspace)
	var mu sync.Mutex
	var models []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var request struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(body, &request)
		mu.Lock()
		models = append(models, request.Model)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"reply"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	t.Cleanup(server.Close)
	assert.NoErr(t, gkconfig.Set("model", "fake/m1"))
	assert.NoErr(t, gkconfig.Set("providers", map[string]any{"fake": map[string]any{
		"type": "openai", "api_key": "k", "base_url": server.URL,
		"models": map[string]any{"m1": map[string]any{"context_window": 128000}, "m2": map[string]any{"context_window": 64000, "max_output_tokens": 42}},
	}}))

	var stdout, stderr bytes.Buffer
	err := RunPersonalCLI(context.Background(), "chat", []string{},
		strings.NewReader(":model\n:model fake/m2\n:model\nhello\n:quit\n"), &stdout, &stderr)
	assert.NoErr(t, err)
	assert.StrContains(t, stdout.String(), "当前模型：fake/m1")
	assert.StrContains(t, stdout.String(), "fake/m2")
	assert.StrContains(t, stdout.String(), "输出上限 max_output_tokens：42")
	assert.StrContains(t, stdout.String(), "已切换模型：fake/m2（下一回合生效）")
	mu.Lock()
	assert.Eq(t, []string{"m2"}, models)
	mu.Unlock()
	session := regexp.MustCompile(`session id: ((?:sess_|sg_|s_)[^\s]+)`).FindStringSubmatch(stderr.String())
	assert.Require(t, assert.Len(t, session, 2))

	stdout.Reset()
	stderr.Reset()
	err = RunPersonalCLI(context.Background(), "chat", []string{"--resume", session[1]},
		strings.NewReader("resumed\n:quit\n"), &stdout, &stderr)
	assert.NoErr(t, err)
	mu.Lock()
	assert.Eq(t, []string{"m2", "m2"}, models)
	mu.Unlock()

	stdout.Reset()
	stderr.Reset()
	err = RunPersonalCLI(context.Background(), "chat", []string{"--resume", session[1], "--model", "fake/m1"},
		strings.NewReader("explicit\n:quit\n"), &stdout, &stderr)
	assert.NoErr(t, err)
	mu.Lock()
	assert.Eq(t, []string{"m2", "m2", "m1"}, models)
	mu.Unlock()
}

func TestChatModelCommandRejectsUnknownModel(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	t.Setenv("SUGA_HOME", shortTestHome(t))
	workspace := t.TempDir()
	t.Chdir(workspace)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"reply"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(server.Close)
	assert.NoErr(t, gkconfig.Set("model", "fake/m1"))
	assert.NoErr(t, gkconfig.Set("providers", map[string]any{"fake": map[string]any{
		"type": "openai", "api_key": "k", "base_url": server.URL,
		"models": map[string]any{"m1": map[string]any{}, "m2": map[string]any{}},
	}}))
	var stdout, stderr bytes.Buffer
	err := RunPersonalCLI(context.Background(), "chat", []string{},
		strings.NewReader(":model fake/nope\n:quit\n"), &stdout, &stderr)
	assert.NoErr(t, err)
	assert.StrContains(t, stdout.String(), "模型切换失败")
	assert.StrContains(t, stdout.String(), "fake/m1, fake/m2")
}
