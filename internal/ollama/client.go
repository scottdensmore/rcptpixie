// Package ollama is the only place in rcptpixie that talks to the Ollama HTTP API.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultModel = "gemma4:e2b"
	DefaultHost  = "http://localhost:11434"

	defaultPort      = "11434"
	httpsPort        = "443"
	defaultTimeout   = 5 * time.Minute
	preflightTimeout = 5 * time.Second
	maxErrorBody     = 500
)

type Client struct {
	base *url.URL
	hc   *http.Client
	log  *slog.Logger
}

// New resolves host into an absolute base URL, accepting every form ollama's own
// envconfig does: a bare "host" or "host:port" is assumed to be http, ":port"
// alone means localhost, and a missing port becomes 11434 (443 under https).
func New(host string, timeout time.Duration, log *slog.Logger) (*Client, error) {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	h := strings.TrimSpace(host)
	if h == "" {
		h = DefaultHost
	}
	if !strings.Contains(h, "://") {
		h = "http://" + bracketIPv6(h)
	}
	h = strings.TrimRight(h, "/")

	u, err := url.Parse(h)
	if err != nil {
		return nil, fmt.Errorf("invalid ollama host %q: %w", host, err)
	}
	switch {
	case u.Hostname() != "":
	case u.Port() != "":
		// ":11434" is what ollama's own client builds; an empty host means
		// localhost, so rescue it instead of refusing to start.
		u.Host = net.JoinHostPort("localhost", u.Port())
	default:
		return nil, fmt.Errorf("invalid ollama host %q: no host in URL", host)
	}
	if u.Port() == "" {
		// An explicit https URL belongs on the scheme's own default port: a
		// TLS-terminating proxy is never on 11434.
		port := defaultPort
		if u.Scheme == "https" {
			port = httpsPort
		}
		u.Host = net.JoinHostPort(u.Hostname(), port)
	}

	return &Client{base: u, hc: &http.Client{Timeout: timeout}, log: log}, nil
}

// bracketIPv6 accepts a bare "::1" the way ollama's envconfig does; url.Parse
// only understands the bracketed form.
func bracketIPv6(h string) string {
	if strings.Contains(h, ":") && !strings.HasPrefix(h, "[") && net.ParseIP(h) != nil {
		return "[" + h + "]"
	}
	return h
}

// Host is the resolved base URL, e.g. "http://localhost:11434".
func (c *Client) Host() string { return c.base.String() }

type GenerateRequest struct {
	Model   string          `json:"model"`
	Prompt  string          `json:"prompt"`
	System  string          `json:"system,omitempty"`
	Images  []string        `json:"images,omitempty"`
	Format  json.RawMessage `json:"format,omitempty"`
	Options map[string]any  `json:"options,omitempty"`
	Stream  bool            `json:"stream"`
}

type generateResponse struct {
	Response   string `json:"response"`
	Done       bool   `json:"done"`
	DoneReason string `json:"done_reason"`
	Error      string `json:"error"`
}

func (c *Client) Generate(ctx context.Context, req GenerateRequest) (string, error) {
	req.Stream = false

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("encoding ollama request: %w", err)
	}

	endpoint := c.base.JoinPath("api", "generate").String()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("building ollama request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	c.log.Debug("ollama generate", "model", req.Model, "prompt_bytes", len(req.Prompt), "images", len(req.Images))

	resp, err := c.hc.Do(httpReq)
	if err != nil {
		return "", c.transportError(ctx, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return "", &ModelNotFoundError{Model: req.Model}
	case resp.StatusCode != http.StatusOK:
		return "", &APIError{Status: resp.StatusCode, Message: errorMessage(resp.Body)}
	}

	// Stream is false so the primary path is a single object; the loop is a
	// defensive fallback for proxies or older servers that still chunk.
	dec := json.NewDecoder(resp.Body)
	var (
		out  strings.Builder
		last generateResponse
	)
	for {
		var gr generateResponse
		if err := dec.Decode(&gr); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return "", ctxErr
			}
			return "", fmt.Errorf("decoding ollama response: %w", err)
		}
		if gr.Error != "" {
			return "", &APIError{Status: http.StatusOK, Message: gr.Error}
		}
		out.WriteString(gr.Response)
		last = gr
		if !dec.More() {
			break
		}
	}

	if strings.TrimSpace(out.String()) == "" {
		return "", ErrEmptyResponse
	}
	c.log.Debug("ollama response", "bytes", out.Len(), "done", last.Done, "done_reason", last.DoneReason)
	return out.String(), nil
}

func (c *Client) Tags(ctx context.Context) ([]string, error) {
	endpoint := c.base.JoinPath("api", "tags").String()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("building ollama request: %w", err)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, c.transportError(ctx, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, &APIError{Status: resp.StatusCode, Message: errorMessage(resp.Body)}
	}

	var payload struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decoding ollama tags: %w", err)
	}

	names := make([]string, 0, len(payload.Models))
	for _, m := range payload.Models {
		if m.Name != "" {
			names = append(names, m.Name)
		}
	}
	return names, nil
}

// Preflight reports whether model is installed, using its own short deadline so
// an unreachable server fails in milliseconds rather than after the generate
// timeout.
func (c *Client) Preflight(ctx context.Context, model string) error {
	ctx, cancel := context.WithTimeout(ctx, preflightTimeout)
	defer cancel()

	installed, err := c.Tags(ctx)
	if err != nil {
		var apiErr *APIError
		var unreachable *UnreachableError
		if errors.As(err, &apiErr) || errors.As(err, &unreachable) {
			return err
		}
		return &UnreachableError{Host: c.base.String(), Err: err}
	}

	want := normalizeModel(model)
	for _, name := range installed {
		if normalizeModel(name) == want {
			return nil
		}
	}
	return &ModelNotFoundError{Model: model, Available: installed}
}

// normalizeModel makes a bare name comparable to a tagged one. Matching is
// exact after normalisation: "gemma4" must never match "gemma4:31b".
func normalizeModel(m string) string {
	m = strings.ToLower(strings.TrimSpace(m))
	if m != "" && !strings.Contains(m, ":") {
		m += ":latest"
	}
	return m
}

func (c *Client) transportError(ctx context.Context, err error) error {
	if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("ollama request failed: %w", err)
	}
	return &UnreachableError{Host: c.base.String(), Err: err}
}

func errorMessage(r io.Reader) string {
	b, err := io.ReadAll(io.LimitReader(r, maxErrorBody))
	if err != nil {
		return err.Error()
	}
	return strings.TrimSpace(string(b))
}

var ErrEmptyResponse = errors.New("ollama returned an empty response")

type UnreachableError struct {
	Host string
	Err  error
}

func (e *UnreachableError) Error() string {
	return fmt.Sprintf("cannot reach ollama at %s: %v\n  Is it running? Start it with: ollama serve\n  Different host? Set OLLAMA_HOST or pass -host http://HOST:PORT", e.Host, e.Err)
}

func (e *UnreachableError) Unwrap() error { return e.Err }

type ModelNotFoundError struct {
	Model     string
	Available []string
}

func (e *ModelNotFoundError) Error() string {
	msg := fmt.Sprintf("ollama model %q is not installed\n  Run: ollama pull %s", e.Model, e.Model)
	if len(e.Available) > 0 {
		msg += "\n  Installed: " + strings.Join(e.Available, ", ")
	}
	return msg
}

type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("ollama returned HTTP %d: %s", e.Status, e.Message)
}
