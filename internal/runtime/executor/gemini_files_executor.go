package executor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
)

const (
	geminiFilesActionKey       = "gemini.files.action"
	geminiFilesNameKey         = "gemini.files.name"
	cliproxyBodyReaderKey      = "_cliproxy_body_reader"
	cliproxyBodyLengthKey      = "_cliproxy_body_length"
	cliproxyResponseStatusKey  = "_cliproxy_status_code"
	cliproxyResponseHeadersKey = "_cliproxy_headers"
)

func geminiFilesAction(meta map[string]any) string {
	if len(meta) == 0 {
		return ""
	}
	v, _ := meta[geminiFilesActionKey].(string)
	return strings.TrimSpace(v)
}

func (e *GeminiExecutor) executeFiles(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (resp cliproxyexecutor.Response, err error) {
	apiKey, bearer := geminiCreds(auth)
	action := geminiFilesAction(req.Metadata)
	if action == "" {
		return resp, fmt.Errorf("gemini files: missing action")
	}

	method, endpoint, body, errBuild := e.buildGeminiFilesRequest(action, auth, req, opts)
	if errBuild != nil {
		return resp, errBuild
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return resp, err
	}
	applyGeminiFilesBodyLength(httpReq, req.Metadata)
	applyGeminiFilesHeaders(httpReq.Header, opts.Headers)
	if apiKey != "" {
		httpReq.Header.Set("x-goog-api-key", apiKey)
	} else if bearer != "" {
		httpReq.Header.Set("Authorization", "Bearer "+bearer)
	}
	applyGeminiHeaders(httpReq, auth)

	httpClient := newProxyAwareHTTPClient(ctx, e.cfg, auth, 0)
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		return resp, err
	}
	defer func() { _ = httpResp.Body.Close() }()

	resp.Metadata = map[string]any{
		cliproxyResponseStatusKey:  httpResp.StatusCode,
		cliproxyResponseHeadersKey: httpResp.Header.Clone(),
	}

	data, _ := io.ReadAll(httpResp.Body)
	resp.Payload = data
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return cliproxyexecutor.Response{}, statusErr{code: httpResp.StatusCode, msg: string(data), headers: httpResp.Header.Clone()}
	}
	return resp, nil
}

func (e *GeminiExecutor) buildGeminiFilesRequest(action string, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (string, string, io.Reader, error) {
	if action == "files.upload" {
		if raw, ok := opts.Metadata[cliproxyexecutor.GeminiFilesUploadURLMetadataKey].(string); ok {
			if v := strings.TrimSpace(raw); v != "" {
				return http.MethodPost, v, geminiFilesBody(req), nil
			}
		}
	}

	baseURL := resolveGeminiBaseURL(auth)
	if strings.TrimSpace(baseURL) == "" {
		return "", "", nil, statusErr{code: http.StatusServiceUnavailable, msg: "gemini files: missing base url"}
	}
	baseURL = strings.TrimSuffix(baseURL, "/")

	switch action {
	case "files.upload":
		return http.MethodPost, addQuery(baseURL+"/upload/"+glAPIVersion+"/files", opts.Query), geminiFilesBody(req), nil
	case "files.list":
		return http.MethodGet, addQuery(baseURL+"/"+glAPIVersion+"/files", opts.Query), nil, nil
	case "files.get":
		name := normalizeGeminiFileName(req.Metadata)
		return http.MethodGet, addQuery(baseURL+"/"+glAPIVersion+"/"+name, opts.Query), nil, nil
	case "files.delete":
		name := normalizeGeminiFileName(req.Metadata)
		return http.MethodDelete, addQuery(baseURL+"/"+glAPIVersion+"/"+name, opts.Query), nil, nil
	default:
		return "", "", nil, statusErr{code: http.StatusBadRequest, msg: "gemini files: unsupported action"}
	}
}

func geminiFilesBody(req cliproxyexecutor.Request) io.Reader {
	if r, ok := req.Metadata[cliproxyBodyReaderKey].(io.Reader); ok && r != nil {
		return r
	}
	return bytes.NewReader(req.Payload)
}

func applyGeminiFilesBodyLength(req *http.Request, meta map[string]any) {
	if req == nil || len(meta) == 0 {
		return
	}
	v, ok := meta[cliproxyBodyLengthKey]
	if !ok {
		return
	}
	if n, ok := v.(int64); ok && n >= 0 {
		req.ContentLength = n
	}
}

func normalizeGeminiFileName(meta map[string]any) string {
	if len(meta) == 0 {
		return "files/"
	}
	name, _ := meta[geminiFilesNameKey].(string)
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "/")
	if strings.HasPrefix(name, "files/") {
		return name
	}
	if name == "" {
		return "files/"
	}
	return "files/" + name
}

func addQuery(rawURL string, q url.Values) string {
	if len(q) == 0 {
		return rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil || u == nil {
		return rawURL
	}
	query := u.Query()
	for k, vals := range q {
		for _, v := range vals {
			query.Add(k, v)
		}
	}
	u.RawQuery = query.Encode()
	return u.String()
}

func applyGeminiFilesHeaders(dst, src http.Header) {
	if dst == nil || src == nil {
		return
	}
	for k, vals := range src {
		if strings.EqualFold(k, "Authorization") || strings.EqualFold(k, "X-Goog-Api-Key") {
			continue
		}
		if strings.EqualFold(k, "Host") || strings.EqualFold(k, "Content-Length") {
			continue
		}
		for _, v := range vals {
			dst.Add(k, v)
		}
	}
}
