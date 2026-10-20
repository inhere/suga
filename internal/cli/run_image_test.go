package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// testRunPNG encodes a small 4×2 picture (big enough for the view_image
// pipeline chat :image and run --image share, small enough for tests); seed
// picks the colour of the first pixel, so two calls give different bytes.
func testRunPNG(t *testing.T, seed uint8) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 2))
	img.Set(0, 0, color.RGBA{R: seed, G: 255 - seed, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// writeRunPNG writes one picture below dir and returns its absolute path.
func writeRunPNG(t *testing.T, dir, name string, seed uint8) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, testRunPNG(t, seed), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// pipelineImageReader loads one picture exactly the way wiring.ReadImage does:
// the view_image rules against a workspace plus PrepareImage's identification
// and scaling (the max side is the tool's default for the standard window).
func pipelineImageReader(workspace string) func(string) (agentcore.ImagePart, string, error) {
	return func(path string) (agentcore.ImagePart, string, error) {
		data, name, err := native.ReadImageFile(workspace, nil, nil, path)
		if err != nil {
			return agentcore.ImagePart{}, "", err
		}
		part, _, err := native.PrepareImage(data, name, 2048)
		return part, path, err
	}
}

// captureImageProvider records the requests the runtime sent, so a test can
// assert the pictures reached the model's user message.
type captureImageProvider struct {
	captured []*agentcoreapi.GenerateRequest
}

func (p *captureImageProvider) Name() string { return "capture" }

func (p *captureImageProvider) Generate(_ context.Context, req *agentcoreapi.GenerateRequest) (*agentcoreapi.GenerateResult, error) {
	p.captured = append(p.captured, req)
	return &agentcoreapi.GenerateResult{Content: "seen", FinishReason: agentcoreapi.FinishFinal}, nil
}

// fakeGate is the stand-in of the runtime's vision gate.
type fakeGate struct{ visible bool }

func (g fakeGate) CanSee() bool { return g.visible }

func TestLoadRunImages(t *testing.T) {
	dir := t.TempDir()
	reader := pipelineImageReader(dir)
	first := writeRunPNG(t, dir, "first.png", 10)
	second := writeRunPNG(t, dir, "second.png", 200)

	t.Run("several pictures keep their order", func(t *testing.T) {
		parts, err := loadRunImages(reader, fakeGate{visible: true}, []string{first, second}, "prov/m1")
		assert.NoErr(t, err)
		assert.Eq(t, 2, len(parts))
		assert.Eq(t, "image/png", parts[0].MediaType)
		assert.Eq(t, "image/png", parts[1].MediaType)
		// The parts carry their (different) bytes in memory, order kept.
		assert.Eq(t, true, len(parts[0].Data) > 0)
		assert.Eq(t, true, len(parts[1].Data) > 0)
		assert.Eq(t, false, bytes.Equal(parts[0].Data, parts[1].Data))
	})
	t.Run("a relative path resolves against the run workspace", func(t *testing.T) {
		// run hands the reader the session workspace (the run's working
		// directory), so "--image shots/a.png" means <run cwd>/shots/a.png.
		parts, err := loadRunImages(reader, fakeGate{visible: true}, []string{"first.png"}, "prov/m1")
		assert.NoErr(t, err)
		assert.Eq(t, 1, len(parts))
		assert.Eq(t, "image/png", parts[0].MediaType)
	})
	t.Run("a blind model fails the load", func(t *testing.T) {
		_, err := loadRunImages(reader, fakeGate{visible: false}, []string{first}, "prov/qwen")
		assert.Require(t, err != nil)
		assert.Eq(t, true, strings.Contains(err.Error(), "不支持看图"))
		assert.Eq(t, true, strings.Contains(err.Error(), "--model"))
		assert.Eq(t, true, strings.Contains(err.Error(), "vision"))
	})
	t.Run("too many pictures fail the load", func(t *testing.T) {
		paths := make([]string, maxRunImages+1)
		for i := range paths {
			paths[i] = first
		}
		_, err := loadRunImages(reader, fakeGate{visible: true}, paths, "prov/m1")
		assert.Eq(t, true, err != nil && strings.Contains(err.Error(), "最多 16 张"))
	})
	t.Run("one unreadable picture names it and fails", func(t *testing.T) {
		bad := filepath.Join(dir, "missing.png")
		_, err := loadRunImages(reader, fakeGate{visible: true}, []string{first, bad}, "prov/m1")
		assert.Require(t, err != nil)
		assert.Eq(t, true, strings.Contains(err.Error(), "第 2 个"))
		assert.Eq(t, true, strings.Contains(err.Error(), bad))
	})
}

func TestRunImageRejectsOnChatMode(t *testing.T) {
	var stderr bytes.Buffer
	err := RunPersonalCLI(context.Background(), "chat", []string{"--image", "a.png"},
		strings.NewReader(""), &bytes.Buffer{}, &stderr)
	assert.Eq(t, true, err != nil && strings.Contains(err.Error(), "--image is only supported in run mode"))
}

// TestRunImageReachesTheRequest pins the whole run path: the pictures prepared
// with the :image pipeline go with the user message to the provider, their
// files land below sessions/<id>/images/, and stream-json's first line counts
// them.
func TestRunImageReachesTheRequest(t *testing.T) {
	prov := &captureImageProvider{}
	// The pictures resolve and are stored against the sessions root, so this
	// runner is built with one (newCLITestRunner leaves SessionsDir empty).
	dbPath := newTestStorePath(t)
	sessionsDir := filepath.Join(filepath.Dir(filepath.Dir(dbPath)), "sessions")
	repo, err := store.Open(dbPath)
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = repo.Close() })
	runner, err := personal.New(personal.Config{
		Repository: repo, Provider: prov, SessionsDir: sessionsDir,
	})
	assert.Require(t, assert.NoErr(t, err))
	// The source pictures live next to the sessions root.
	dir := filepath.Join(filepath.Dir(dbPath), "pics")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	reader := pipelineImageReader(dir)
	parts, err := loadRunImages(reader, nil, []string{
		writeRunPNG(t, dir, "one.png", 10),
		writeRunPNG(t, dir, "two.png", 200),
	}, "")
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, false, bytes.Equal(parts[0].Data, parts[1].Data), "the two pictures differ")

	var stdout, stderr bytes.Buffer
	err = executePersonalRun(context.Background(), runner, personalOptions{
		Prompt: "describe the pictures", SessionID: "img-run",
		OutputFormat: "stream-json", Images: parts,
	}, &stdout, &stderr)
	assert.NoErr(t, err)

	// stream-json: the session event of the first line carries the count.
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	var session map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &session); err != nil {
		t.Fatal(err)
	}
	assert.Eq(t, "session", session["type"])
	assert.Eq(t, float64(2), session["images"])

	// The pictures reached the model: the request's user message carries both
	// parts with their bytes.
	assert.Eq(t, 1, len(prov.captured))
	var user *agentcoreapi.Message
	for i := range prov.captured[0].Messages {
		if prov.captured[0].Messages[i].Role == "user" {
			user = &prov.captured[0].Messages[i]
		}
	}
	if user == nil {
		t.Fatal("no user message in the request")
	}
	assert.Eq(t, 2, len(user.Images))
	assert.Eq(t, true, strings.Contains(user.Content, "describe the pictures"))

	// The request carries the pictures' bytes in memory; the store kept one
	// file per distinct picture below sessions/img-run/images/.
	assert.Eq(t, true, len(user.Images[0].Data) > 0)
	assert.Eq(t, true, len(user.Images[1].Data) > 0)
	assert.Eq(t, false, bytes.Equal(user.Images[0].Data, user.Images[1].Data), "both pictures reached the request")
	entries, err := os.ReadDir(store.SessionImagesDir(sessionsDir, "img-run"))
	assert.NoErr(t, err)
	assert.Eq(t, 2, len(entries))
}

// TestRunImageWithoutFlagKeepsStreamJSON pins the "no --image" behaviour: the
// session line carries no images field at all.
func TestRunImageWithoutFlagKeepsStreamJSON(t *testing.T) {
	runner, _ := newCLITestRunner(t, &captureImageProvider{})
	var stdout, stderr bytes.Buffer
	err := executePersonalRun(context.Background(), runner, personalOptions{
		Prompt: "plain", OutputFormat: "stream-json",
	}, &stdout, &stderr)
	assert.NoErr(t, err)
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	var session map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &session); err != nil {
		t.Fatal(err)
	}
	_, has := session["images"]
	assert.Eq(t, false, has)
	assert.Eq(t, false, strings.Contains(lines[0], "images"))
}

// imageCLIHome isolates SUGA_HOME for a test that drives the whole CLI: the
// config is set through gkconfig, the session store and the image files go into
// the temp home, never into ~/.suga.
func imageCLIHome(t *testing.T) {
	t.Helper()
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SUGA_HOME", "") // fall back to HOME/.suga despite the TestMain default
	t.Setenv("SUGA_MODEL", "")
}

// requestedImages returns the data: URLs of the image parts of the request's
// user message, in message order (the openai mapping of one message).
func requestedImages(t *testing.T, body map[string]any) []string {
	t.Helper()
	messages, _ := body["messages"].([]any)
	var urls []string
	for _, raw := range messages {
		message, _ := raw.(map[string]any)
		if message["role"] != "user" {
			continue
		}
		parts, ok := message["content"].([]any)
		if !ok {
			continue
		}
		for _, rawPart := range parts {
			part, _ := rawPart.(map[string]any)
			if part["type"] != "image_url" {
				continue
			}
			image, _ := part["image_url"].(map[string]any)
			url, _ := image["url"].(string)
			urls = append(urls, url)
		}
	}
	return urls
}

// TestRunImageThroughCLI drives the flag itself: --image values (relative paths
// against the run's working directory) are prepared in order and reach the
// provider request as the image parts of this turn's user message.
func TestRunImageThroughCLI(t *testing.T) {
	imageCLIHome(t)
	workspace := t.TempDir()
	t.Chdir(workspace)
	_ = gkconfig.Set("workspace", workspace)
	_ = gkconfig.Set("model", "openai/gpt-4o")

	var mu sync.Mutex
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode provider request: %v", err)
		}
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"seen"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(server.Close)
	setTestProvider(t, server.URL)

	// Both files live in the run's working directory and are named relatively.
	first := writeRunPNG(t, workspace, "first.png", 10)
	second := writeRunPNG(t, workspace, "second.png", 200)
	read := pipelineImageReader(workspace)
	wantParts := make([]string, 0, 2)
	for _, name := range []string{"first.png", "second.png"} {
		part, _, err := read(name)
		assert.Require(t, assert.NoErr(t, err))
		wantParts = append(wantParts, "data:"+part.MediaType+";base64,"+base64.StdEncoding.EncodeToString(part.Data))
	}

	var stdout, stderr bytes.Buffer
	err := RunPersonalCLI(context.Background(), "run", []string{
		"--prompt", "describe", "--session-id", "img-cli", "--output-format", "stream-json",
		"--image", "first.png", "--image", "second.png",
	}, strings.NewReader(""), &stdout, &stderr)
	assert.NoErr(t, err)

	// stream-json: the session line counts both pictures.
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	var session map[string]any
	assert.Require(t, assert.NoErr(t, json.Unmarshal([]byte(lines[0]), &session)))
	assert.Eq(t, float64(2), session["images"])

	// The provider got both pictures in the declared order.
	mu.Lock()
	defer mu.Unlock()
	assert.Require(t, assert.Eq(t, 1, len(bodies)))
	assert.Eq(t, wantParts, requestedImages(t, bodies[0]))
	// Both files were stored below the session's images directory.
	home, err := os.UserHomeDir()
	assert.Require(t, assert.NoErr(t, err))
	stored, err := os.ReadDir(store.SessionImagesDir(filepath.Join(home, ".suga", "sessions"), "img-cli"))
	assert.NoErr(t, err)
	assert.Eq(t, 2, len(stored))
	assert.Eq(t, true, first != second)
}

// TestRunImageWithoutVisionFailsTheCLI pins item 1's exit: a model that cannot
// see makes the run fail before any model request, with the --model / vision
// hint.
func TestRunImageWithoutVisionFailsTheCLI(t *testing.T) {
	imageCLIHome(t)
	workspace := t.TempDir()
	t.Chdir(workspace)
	_ = gkconfig.Set("workspace", workspace)
	// qwen is inferred text-only.
	_ = gkconfig.Set("model", "openai/qwen3")

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"seen"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(server.Close)
	setTestProvider(t, server.URL)
	writeRunPNG(t, workspace, "shot.png", 10)

	var stdout, stderr bytes.Buffer
	err := RunPersonalCLI(context.Background(), "run", []string{
		"--prompt", "describe", "--session-id", "img-blind", "--image", "shot.png",
	}, strings.NewReader(""), &stdout, &stderr)
	assert.Require(t, assert.NotNil(t, err))
	assert.StrContains(t, err.Error(), "不支持看图")
	assert.StrContains(t, err.Error(), "--model")
	assert.StrContains(t, err.Error(), "vision")
	assert.Eq(t, 1, ExitCode(err))
	// No request was sent and no turn was created.
	assert.Eq(t, 0, requests)
}
