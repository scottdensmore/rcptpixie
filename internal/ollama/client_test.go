package ollama_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/scottdensmore/rcptpixie/v2/internal/ollama"
	"github.com/scottdensmore/rcptpixie/v2/internal/testutil"
)

func newClient(t *testing.T, host string, timeout time.Duration) *ollama.Client {
	t.Helper()
	c, err := ollama.New(host, timeout, nil)
	if err != nil {
		t.Fatalf("New(%q): %v", host, err)
	}
	return c
}

// TestGenerateSendsStreamFalse is the regression test for the shipped NDJSON
// bug: the request omitted stream, Ollama defaulted to true, and the reply was
// a stream of objects the decoder could not read.
func TestGenerateSendsStreamFalse(t *testing.T) {
	t.Parallel()

	fake := testutil.NewFake(t, nil, `{"ok":true}`)
	c := newClient(t, fake.URL, 0)

	got, err := c.Generate(context.Background(), ollama.GenerateRequest{Model: "gemma4:e2b", Prompt: "hi", Stream: true})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got != `{"ok":true}` {
		t.Errorf("response = %q", got)
	}

	body := fake.Requests[0]
	v, ok := body["stream"]
	if !ok {
		t.Fatal(`request has no "stream" key`)
	}
	if v != false {
		t.Errorf(`"stream" = %v, want false`, v)
	}
}

func TestGenerateSendsSchemaAndOptions(t *testing.T) {
	t.Parallel()

	schema := json.RawMessage(`{"type":"object","properties":{"vendor":{"type":"string"}},"required":["vendor"]}`)
	fake := testutil.NewFake(t, nil, `{"vendor":"Acme"}`)
	c := newClient(t, fake.URL, 0)

	_, err := c.Generate(context.Background(), ollama.GenerateRequest{
		Model:  "gemma4:e2b",
		Prompt: "hi",
		System: "be terse",
		Format: schema,
		Options: map[string]any{
			"temperature": 0,
			"num_ctx":     8192,
			"num_predict": 256,
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	body := fake.Requests[0]

	var want any
	if err := json.Unmarshal(schema, &want); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	if !reflect.DeepEqual(body["format"], want) {
		t.Errorf("format = %#v, want %#v", body["format"], want)
	}

	opts, ok := body["options"].(map[string]any)
	if !ok {
		t.Fatalf("options = %#v, want an object", body["options"])
	}
	for k, want := range map[string]float64{"temperature": 0, "num_ctx": 8192, "num_predict": 256} {
		if got, ok := opts[k].(float64); !ok || got != want {
			t.Errorf("options[%q] = %#v, want %v", k, opts[k], want)
		}
	}
	if body["system"] != "be terse" {
		t.Errorf("system = %#v", body["system"])
	}
}

func TestGenerateImagesAreBareBase64(t *testing.T) {
	t.Parallel()

	raw := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0xff}
	encoded := base64.StdEncoding.EncodeToString(raw)

	fake := testutil.NewFake(t, nil, `{}`)
	c := newClient(t, fake.URL, 0)

	_, err := c.Generate(context.Background(), ollama.GenerateRequest{
		Model:  "gemma4:e2b",
		Prompt: "read this",
		Images: []string{encoded, base64.StdEncoding.EncodeToString([]byte("second"))},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	images, ok := fake.Requests[0]["images"].([]any)
	if !ok {
		t.Fatalf("images = %#v, want an array", fake.Requests[0]["images"])
	}
	if len(images) != 2 {
		t.Fatalf("len(images) = %d, want 2", len(images))
	}
	for i, img := range images {
		s, ok := img.(string)
		if !ok {
			t.Fatalf("images[%d] = %#v, want a string", i, img)
		}
		if strings.HasPrefix(s, "data:") {
			t.Errorf("images[%d] carries a data: URI prefix", i)
		}
	}
	decoded, err := base64.StdEncoding.DecodeString(images[0].(string))
	if err != nil {
		t.Fatalf("images[0] is not base64: %v", err)
	}
	if !reflect.DeepEqual(decoded, raw) {
		t.Errorf("images[0] decodes to % x, want % x", decoded, raw)
	}
}

// TestGenerateOmitsImagesWhenNone guards against a literal null, which some
// Ollama builds reject.
func TestGenerateOmitsImagesWhenNone(t *testing.T) {
	t.Parallel()

	fake := testutil.NewFake(t, nil, `{}`)
	c := newClient(t, fake.URL, 0)

	if _, err := c.Generate(context.Background(), ollama.GenerateRequest{Model: "m", Prompt: "hi"}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if v, ok := fake.Requests[0]["images"]; ok {
		t.Errorf(`"images" present as %#v, want absent`, v)
	}
	if v, ok := fake.Requests[0]["format"]; ok {
		t.Errorf(`"format" present as %#v, want absent`, v)
	}
}

func TestGenerateNDJSONFallback(t *testing.T) {
	t.Parallel()

	fake := testutil.NewFakeNDJSON(t, `{"vendor":`, `"Acme"`, `}`)
	c := newClient(t, fake.URL, 0)

	got, err := c.Generate(context.Background(), ollama.GenerateRequest{Model: "m", Prompt: "hi"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if want := `{"vendor":"Acme"}`; got != want {
		t.Errorf("response = %q, want %q", got, want)
	}
}

func TestGenerateErrorInBody200(t *testing.T) {
	t.Parallel()

	fake := testutil.NewFakeStatus(t, http.StatusOK, `{"error":"out of memory"}`)
	c := newClient(t, fake.URL, 0)

	_, err := c.Generate(context.Background(), ollama.GenerateRequest{Model: "m", Prompt: "hi"})
	var apiErr *ollama.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *ollama.APIError", err, err)
	}
	if apiErr.Message != "out of memory" {
		t.Errorf("Message = %q, want %q", apiErr.Message, "out of memory")
	}
}

func TestGenerateEmptyResponse(t *testing.T) {
	t.Parallel()

	fake := testutil.NewFakeStatus(t, http.StatusOK, `{"response":"","done":true}`)
	c := newClient(t, fake.URL, 0)

	_, err := c.Generate(context.Background(), ollama.GenerateRequest{Model: "m", Prompt: "hi"})
	if !errors.Is(err, ollama.ErrEmptyResponse) {
		t.Fatalf("err = %v, want ErrEmptyResponse", err)
	}
}

func TestGenerate404(t *testing.T) {
	t.Parallel()

	fake := testutil.NewFakeStatus(t, http.StatusNotFound, `{"error":"model 'nope' not found"}`)
	c := newClient(t, fake.URL, 0)

	_, err := c.Generate(context.Background(), ollama.GenerateRequest{Model: "nope", Prompt: "hi"})
	var notFound *ollama.ModelNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("err = %v (%T), want *ollama.ModelNotFoundError", err, err)
	}
	if notFound.Model != "nope" {
		t.Errorf("Model = %q, want %q", notFound.Model, "nope")
	}
	if !strings.Contains(err.Error(), "ollama pull nope") {
		t.Errorf("Error() = %q, want it to suggest `ollama pull nope`", err.Error())
	}
}

func TestGenerate500(t *testing.T) {
	t.Parallel()

	fake := testutil.NewFakeStatus(t, http.StatusInternalServerError, "server exploded")
	c := newClient(t, fake.URL, 0)

	_, err := c.Generate(context.Background(), ollama.GenerateRequest{Model: "m", Prompt: "hi"})
	var apiErr *ollama.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *ollama.APIError", err, err)
	}
	if apiErr.Status != http.StatusInternalServerError {
		t.Errorf("Status = %d, want 500", apiErr.Status)
	}
	if !strings.Contains(apiErr.Message, "server exploded") {
		t.Errorf("Message = %q, want it to carry the body", apiErr.Message)
	}
}

func TestGenerateContextCancel(t *testing.T) {
	t.Parallel()

	fake := testutil.NewFakeSlow(t, 30*time.Second)
	c := newClient(t, fake.URL, 0)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for fake.Count() == 0 {
			time.Sleep(time.Millisecond)
		}
		cancel()
	}()

	start := time.Now()
	_, err := c.Generate(ctx, ollama.GenerateRequest{Model: "m", Prompt: "hi"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("Generate took %v to notice the cancellation", elapsed)
	}
}

func TestGenerateTimeout(t *testing.T) {
	t.Parallel()

	fake := testutil.NewFakeSlow(t, 30*time.Second)
	c := newClient(t, fake.URL, 50*time.Millisecond)

	start := time.Now()
	_, err := c.Generate(context.Background(), ollama.GenerateRequest{Model: "m", Prompt: "hi"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want a deadline error", err)
	}
	var unreachable *ollama.UnreachableError
	if errors.As(err, &unreachable) {
		t.Errorf("a timeout was reported as unreachable: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Generate took %v to hit a 50ms timeout", elapsed)
	}
}

// TestPreflightExactMatch is the regression test for the prefix match: "gemma4"
// must not be satisfied by "gemma4:31b".
func TestPreflightExactMatch(t *testing.T) {
	t.Parallel()

	installed := []string{"gemma4:31b", "llama3.2:latest"}

	t.Run("missing tag", func(t *testing.T) {
		t.Parallel()
		fake := testutil.NewFake(t, installed)
		err := newClient(t, fake.URL, 0).Preflight(context.Background(), "gemma4:e2b")
		var notFound *ollama.ModelNotFoundError
		if !errors.As(err, &notFound) {
			t.Fatalf("err = %v (%T), want *ollama.ModelNotFoundError", err, err)
		}
		for _, want := range []string{"ollama pull gemma4:e2b", "gemma4:31b"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("Error() = %q, want it to contain %q", err.Error(), want)
			}
		}
	})

	t.Run("bare name normalises to latest", func(t *testing.T) {
		t.Parallel()
		fake := testutil.NewFake(t, installed)
		if err := newClient(t, fake.URL, 0).Preflight(context.Background(), "llama3.2"); err != nil {
			t.Fatalf("Preflight(llama3.2) = %v, want nil", err)
		}
	})

	t.Run("bare name is not a prefix match", func(t *testing.T) {
		t.Parallel()
		fake := testutil.NewFake(t, installed)
		if err := newClient(t, fake.URL, 0).Preflight(context.Background(), "gemma4"); err == nil {
			t.Fatal("Preflight(gemma4) = nil, want an error: only gemma4:31b is installed")
		}
	})

	t.Run("exact tag", func(t *testing.T) {
		t.Parallel()
		fake := testutil.NewFake(t, installed)
		if err := newClient(t, fake.URL, 0).Preflight(context.Background(), "GEMMA4:31B"); err != nil {
			t.Fatalf("Preflight(GEMMA4:31B) = %v, want nil", err)
		}
	})
}

func TestPreflightUnreachable(t *testing.T) {
	t.Parallel()

	fake := testutil.NewFake(t, []string{"gemma4:e2b"})
	c := newClient(t, fake.URL, 0)
	fake.Server.Close()

	err := c.Preflight(context.Background(), "gemma4:e2b")
	var unreachable *ollama.UnreachableError
	if !errors.As(err, &unreachable) {
		t.Fatalf("err = %v (%T), want *ollama.UnreachableError", err, err)
	}
	if !strings.Contains(err.Error(), "ollama serve") {
		t.Errorf("Error() = %q, want it to suggest `ollama serve`", err.Error())
	}
}

func TestPreflightAPIError(t *testing.T) {
	t.Parallel()

	fake := testutil.NewFakeStatus(t, http.StatusBadGateway, "proxy says no")
	err := newClient(t, fake.URL, 0).Preflight(context.Background(), "gemma4:e2b")

	var apiErr *ollama.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *ollama.APIError", err, err)
	}
	if apiErr.Status != http.StatusBadGateway {
		t.Errorf("Status = %d, want 502", apiErr.Status)
	}
}

// TestNewHostNormalization covers the values a user can put in OLLAMA_HOST or
// -host: bare host, host:port, and a full URL.
func TestNewHostNormalization(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		host string
		want string
	}{
		{"empty falls back to default", "", ollama.DefaultHost},
		{"whitespace only", "   ", ollama.DefaultHost},
		{"host and port", "localhost:11434", "http://localhost:11434"},
		{"bare host gets default port", "127.0.0.1", "http://127.0.0.1:11434"},
		{"bare hostname", "gpu.lan", "http://gpu.lan:11434"},
		{"full url", "http://gpu.lan:11434", "http://gpu.lan:11434"},
		{"url without port", "http://gpu.lan", "http://gpu.lan:11434"},
		{"trailing slash", "http://gpu.lan:11434/", "http://gpu.lan:11434"},
		{"https", "https://ollama.example.com:443", "https://ollama.example.com:443"},
		{"alternate port", "localhost:1234", "http://localhost:1234"},

		// ":11434" is the value ollama's own client puts in OLLAMA_HOST; an
		// empty host means localhost, so it must resolve, not fail.
		{"port only", ":11434", "http://localhost:11434"},
		{"port only alternate", ":1234", "http://localhost:1234"},
		{"port only with scheme", "http://:11434", "http://localhost:11434"},
		{"port only trailing slash", ":11434/", "http://localhost:11434"},

		// An https URL without a port belongs on 443, not on 11434.
		{"https without port", "https://ollama.example.com", "https://ollama.example.com:443"},
		{"https trailing slash", "https://ollama.example.com/", "https://ollama.example.com:443"},
		{"https keeps explicit port", "https://ollama.example.com:8443", "https://ollama.example.com:8443"},

		{"ipv6 literal", "[::1]:11434", "http://[::1]:11434"},
		{"ipv6 literal without port", "[::1]", "http://[::1]:11434"},
		{"ipv6 literal unbracketed", "::1", "http://[::1]:11434"},
		{"ipv6 url", "http://[fe80::1]:1234", "http://[fe80::1]:1234"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, err := ollama.New(tt.host, 0, nil)
			if err != nil {
				t.Fatalf("New(%q): %v", tt.host, err)
			}
			if got := c.Host(); got != tt.want {
				t.Errorf("New(%q).Host() = %q, want %q", tt.host, got, tt.want)
			}
		})
	}
}

// TestNewRejectsHostWithoutHostname covers the forms that name neither a host
// nor a port; ":port" is not one of them, see TestNewHostNormalization.
func TestNewRejectsHostWithoutHostname(t *testing.T) {
	t.Parallel()

	for _, host := range []string{"http://", "https://", "http:///", "http://:"} {
		if c, err := ollama.New(host, 0, nil); err == nil {
			t.Errorf("New(%q) = %q, want an error", host, c.Host())
		}
	}
}

// TestNewPortOnlyHostReachesServer proves the ":port" rewrite is not cosmetic:
// requests must actually land on the local server.
func TestNewPortOnlyHostReachesServer(t *testing.T) {
	t.Parallel()

	fake := testutil.NewFake(t, []string{"gemma4:e2b"})
	u, err := url.Parse(fake.URL)
	if err != nil {
		t.Fatalf("parse fake URL: %v", err)
	}

	c := newClient(t, ":"+u.Port(), 0)
	if err := c.Preflight(context.Background(), "gemma4:e2b"); err != nil {
		t.Fatalf("Preflight via %q: %v", c.Host(), err)
	}
}
