package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
)

const (
	geminiFilesActionKey       = "gemini.files.action"
	geminiFilesNameKey         = "gemini.files.name"
	cliproxyBodyReaderKey      = "_cliproxy_body_reader"
	cliproxyBodyLengthKey      = "_cliproxy_body_length"
	cliproxyResponseStatusKey  = "_cliproxy_status_code"
	cliproxyResponseHeadersKey = "_cliproxy_headers"
)

type uploadSession struct {
	authID    string
	expiresAt time.Time
}

// GeminiUpstreamFileAPIHandler proxies Gemini Files API calls to the upstream Gemini API.
// It keeps resumable sessions tied to the selected upstream credential, so follow-up calls
// continue to use the same auth.
type GeminiUpstreamFileAPIHandler struct {
	base *handlers.BaseAPIHandler

	mu       sync.Mutex
	sessions map[string]uploadSession
}

func NewGeminiUpstreamFileAPIHandler(base *handlers.BaseAPIHandler) *GeminiUpstreamFileAPIHandler {
	return &GeminiUpstreamFileAPIHandler{base: base}
}

func (h *GeminiUpstreamFileAPIHandler) UploadFile(c *gin.Context) {
	if c == nil || c.Request == nil {
		return
	}
	protocol := c.GetHeader("X-Goog-Upload-Protocol")
	if strings.EqualFold(protocol, "resumable") {
		h.handleResumableUpload(c)
		return
	}
	h.handleMultipartUpload(c)
}

func (h *GeminiUpstreamFileAPIHandler) GetFile(c *gin.Context) {
	h.proxyFileMetadata(c, http.MethodGet)
}
func (h *GeminiUpstreamFileAPIHandler) ListFiles(c *gin.Context) {
	h.proxyFileMetadata(c, http.MethodGet)
}
func (h *GeminiUpstreamFileAPIHandler) DeleteFile(c *gin.Context) {
	h.proxyFileMetadata(c, http.MethodDelete)
}

func (h *GeminiUpstreamFileAPIHandler) handleMultipartUpload(c *gin.Context) {
	opts := fileOptionsFromRequest(c)
	auth, errPick := h.pickAuth(c.Request.Context(), opts)
	if errPick != nil {
		writeProxyError(c, errPick)
		return
	}
	req := coreexecutor.Request{
		Metadata: map[string]any{
			geminiFilesActionKey:  "files.upload",
			cliproxyBodyReaderKey: c.Request.Body,
			cliproxyBodyLengthKey: c.Request.ContentLength,
		},
	}
	opts.Metadata = map[string]any{
		coreauth.ForceAuthIDMetadataKey: auth.ID,
		coreauth.NoRetryMetadataKey:     true,
	}
	resp, err := h.execute(c.Request.Context(), req, opts)
	h.writeProxyResponse(c, resp, err)
}

func (h *GeminiUpstreamFileAPIHandler) handleResumableUpload(c *gin.Context) {
	cmd := strings.TrimSpace(c.GetHeader("X-Goog-Upload-Command"))
	if strings.EqualFold(cmd, "start") {
		h.handleResumableStart(c)
		return
	}
	h.handleResumableFollowUp(c, cmd)
}

func (h *GeminiUpstreamFileAPIHandler) handleResumableStart(c *gin.Context) {
	body := readRequestBody(c)
	req := coreexecutor.Request{Metadata: map[string]any{geminiFilesActionKey: "files.upload"}, Payload: body}
	opts := fileOptionsFromRequest(c)
	resp, err := h.execute(c.Request.Context(), req, opts)
	h.writeResumableStartResponse(c, resp, err)
}

func (h *GeminiUpstreamFileAPIHandler) handleResumableFollowUp(c *gin.Context, cmd string) {
	uploadID := strings.TrimSpace(c.Query("upload_id"))
	authID, ok := h.sessionAuthID(uploadID)
	if !ok {
		writeGeminiFileError(c, http.StatusBadRequest, "missing or expired upload_id", "INVALID_ARGUMENT")
		return
	}
	req := coreexecutor.Request{
		Metadata: map[string]any{
			geminiFilesActionKey:  "files.upload",
			cliproxyBodyReaderKey: c.Request.Body,
			cliproxyBodyLengthKey: c.Request.ContentLength,
		},
	}
	opts := fileOptionsFromRequest(c)
	opts.Metadata = map[string]any{
		coreauth.ForceAuthIDMetadataKey: authID,
		coreauth.NoRetryMetadataKey:     true,
	}
	resp, err := h.execute(c.Request.Context(), req, opts)
	if strings.Contains(strings.ToLower(cmd), "finalize") && err == nil {
		h.dropSession(uploadID)
	}
	h.writeProxyResponse(c, resp, err)
}

func (h *GeminiUpstreamFileAPIHandler) pickAuth(ctx context.Context, opts coreexecutor.Options) (*coreauth.Auth, error) {
	if h == nil || h.base == nil || h.base.AuthManager == nil {
		return nil, httpStatusErr{code: http.StatusServiceUnavailable, msg: "gemini files: auth manager is not configured"}
	}
	return h.base.AuthManager.PickAuth(ctx, "gemini", "", opts)
}

func (h *GeminiUpstreamFileAPIHandler) proxyFileMetadata(c *gin.Context, method string) {
	if c == nil || c.Request == nil {
		return
	}
	action := resolveMetadataAction(c, method)
	meta := map[string]any{geminiFilesActionKey: action}
	if action != "files.list" {
		meta[geminiFilesNameKey] = strings.TrimSpace(c.Param("fileId"))
	}
	req := coreexecutor.Request{Metadata: meta}
	opts := fileOptionsFromRequest(c)
	resp, err := h.execute(c.Request.Context(), req, opts)
	h.writeProxyResponse(c, resp, err)
}

func resolveMetadataAction(c *gin.Context, method string) string {
	if c != nil && strings.TrimSpace(c.Param("fileId")) == "" && method == http.MethodGet {
		return "files.list"
	}
	switch method {
	case http.MethodDelete:
		return "files.delete"
	default:
		return "files.get"
	}
}

func (h *GeminiUpstreamFileAPIHandler) execute(ctx context.Context, req coreexecutor.Request, opts coreexecutor.Options) (coreexecutor.Response, error) {
	if h == nil || h.base == nil || h.base.AuthManager == nil {
		return coreexecutor.Response{}, httpStatusErr{code: http.StatusServiceUnavailable, msg: "gemini files: auth manager is not configured"}
	}
	opts.SourceFormat = sdktranslator.FromString("gemini")
	return h.base.AuthManager.Execute(ctx, []string{"gemini"}, req, opts)
}

type httpStatusErr struct {
	code int
	msg  string
}

func (e httpStatusErr) Error() string {
	if e.msg != "" {
		return e.msg
	}
	return http.StatusText(e.code)
}

func (e httpStatusErr) StatusCode() int { return e.code }

func fileOptionsFromRequest(c *gin.Context) coreexecutor.Options {
	headers := cloneHeadersForGeminiFiles(c)
	query := cloneQueryWithoutClientAuth(c)
	return coreexecutor.Options{Headers: headers, Query: query}
}

func cloneQueryWithoutClientAuth(c *gin.Context) url.Values {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return nil
	}
	q := c.Request.URL.Query()
	out := make(url.Values, len(q))
	for k, v := range q {
		if strings.EqualFold(k, "key") || strings.EqualFold(k, "auth_token") {
			continue
		}
		out[k] = append([]string(nil), v...)
	}
	return out
}

func cloneHeadersForGeminiFiles(c *gin.Context) http.Header {
	if c == nil || c.Request == nil {
		return nil
	}
	src := c.Request.Header
	dst := make(http.Header, len(src))
	for k, v := range src {
		if strings.EqualFold(k, "Authorization") || strings.EqualFold(k, "X-Goog-Api-Key") {
			continue
		}
		if strings.EqualFold(k, "Content-Length") || strings.EqualFold(k, "Host") {
			continue
		}
		if strings.HasPrefix(strings.ToLower(k), "x-goog-upload-") || strings.EqualFold(k, "Content-Type") || strings.EqualFold(k, "Accept") {
			dst[k] = append([]string(nil), v...)
		}
	}
	return dst
}

func (h *GeminiUpstreamFileAPIHandler) writeResumableStartResponse(c *gin.Context, resp coreexecutor.Response, err error) {
	if err != nil {
		h.writeProxyResponse(c, resp, err)
		return
	}
	status, hdr := unpackProxyMetadata(resp.Metadata)
	if hdr == nil {
		hdr = make(http.Header)
	}
	uploadURL := hdr.Get("X-Goog-Upload-URL")
	uploadID := extractUploadID(uploadURL)
	if uploadID != "" {
		h.bindSession(uploadID, authIDFromMetadata(resp.Metadata))
		hdr.Set("X-Goog-Upload-URL", buildProxyUploadURL(c, uploadID))
	}
	writeProxyResponseWithHeaders(c, status, hdr, resp.Payload)
}

func (h *GeminiUpstreamFileAPIHandler) writeProxyResponse(c *gin.Context, resp coreexecutor.Response, err error) {
	if err == nil {
		status, hdr := unpackProxyMetadata(resp.Metadata)
		writeProxyResponseWithHeaders(c, status, hdr, resp.Payload)
		return
	}
	writeProxyError(c, err)
}

func writeProxyError(c *gin.Context, err error) {
	status := proxyErrorStatus(err)
	body := []byte(strings.TrimSpace(err.Error()))
	if len(body) == 0 {
		body = []byte(http.StatusText(status))
	}
	if se, ok := err.(interface{ StatusCode() int }); ok && se != nil {
		if code := se.StatusCode(); code > 0 {
			status = code
		}
	}
	if he, ok := err.(interface{ Headers() http.Header }); ok && he != nil {
		if hdr := he.Headers(); hdr != nil {
			applyResponseHeaders(c, hdr)
		}
	}
	if json.Valid(bytes.TrimSpace(body)) {
		c.Data(status, "application/json", body)
		return
	}
	msg := string(body)
	c.JSON(status, gin.H{
		"error": gin.H{
			"code":    status,
			"message": msg,
			"status":  googleStatusForHTTP(status),
		},
		"message": msg,
	})
}

func contentTypeFromBytes(body []byte) string {
	if len(body) == 0 {
		return "application/json"
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		return "application/json"
	}
	return "text/plain; charset=utf-8"
}

func writeProxyResponseWithHeaders(c *gin.Context, status int, hdr http.Header, payload []byte) {
	if status <= 0 {
		status = http.StatusOK
	}
	applyResponseHeaders(c, hdr)
	contentType := ""
	if hdr != nil {
		contentType = hdr.Get("Content-Type")
	}
	if strings.TrimSpace(contentType) == "" {
		contentType = contentTypeFromBytes(payload)
	}
	c.Data(status, contentType, payload)
}

func unpackProxyMetadata(meta map[string]any) (int, http.Header) {
	if len(meta) == 0 {
		return http.StatusOK, nil
	}
	status, _ := meta[cliproxyResponseStatusKey].(int)
	hdr, _ := meta[cliproxyResponseHeadersKey].(http.Header)
	return status, hdr
}

func proxyErrorStatus(err error) int {
	if err == nil {
		return http.StatusInternalServerError
	}
	if se, ok := err.(interface{ StatusCode() int }); ok && se != nil {
		if code := se.StatusCode(); code > 0 {
			return code
		}
	}
	if ae, ok := err.(*coreauth.Error); ok && ae != nil {
		switch ae.Code {
		case "executor_not_found", "auth_not_found":
			return http.StatusServiceUnavailable
		case "provider_not_found":
			return http.StatusBadRequest
		}
	}
	return http.StatusInternalServerError
}

func googleStatusForHTTP(code int) string {
	switch code {
	case http.StatusBadRequest:
		return "INVALID_ARGUMENT"
	case http.StatusUnauthorized:
		return "UNAUTHENTICATED"
	case http.StatusForbidden:
		return "PERMISSION_DENIED"
	case http.StatusNotFound:
		return "NOT_FOUND"
	case http.StatusTooManyRequests:
		return "RESOURCE_EXHAUSTED"
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return "DEADLINE_EXCEEDED"
	case http.StatusServiceUnavailable, http.StatusBadGateway:
		return "UNAVAILABLE"
	}
	if code >= http.StatusInternalServerError {
		return "INTERNAL"
	}
	return "UNKNOWN"
}

func authIDFromMetadata(meta map[string]any) string {
	if len(meta) == 0 {
		return ""
	}
	if v, ok := meta[coreauth.SelectedAuthIDMetadataKey].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func extractUploadID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(u.Query().Get("upload_id"))
}

func buildProxyUploadURL(c *gin.Context, uploadID string) string {
	if c == nil || c.Request == nil {
		return "/upload/v1beta/files?upload_id=" + url.QueryEscape(uploadID)
	}
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	if v := strings.TrimSpace(c.GetHeader("X-Forwarded-Proto")); v != "" {
		scheme = v
	}
	host := strings.TrimSpace(c.GetHeader("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(c.Request.Host)
	}
	return scheme + "://" + host + "/upload/v1beta/files?upload_id=" + url.QueryEscape(uploadID)
}

func applyResponseHeaders(c *gin.Context, hdr http.Header) {
	if c == nil || hdr == nil {
		return
	}
	for k, values := range hdr {
		if shouldSkipProxyResponseHeader(k) {
			continue
		}
		for _, v := range values {
			c.Writer.Header().Add(k, v)
		}
	}
}

func shouldSkipProxyResponseHeader(k string) bool {
	if strings.EqualFold(k, "Content-Length") || strings.EqualFold(k, "Transfer-Encoding") || strings.EqualFold(k, "Connection") {
		return true
	}
	return false
}

func readRequestBody(c *gin.Context) []byte {
	if c == nil || c.Request == nil {
		return nil
	}
	data, _ := c.GetRawData()
	c.Request.Body = io.NopCloser(bytes.NewReader(data))
	return data
}

func writeGeminiFileError(c *gin.Context, status int, message, code string) {
	if status <= 0 {
		status = http.StatusBadRequest
	}
	c.JSON(status, gin.H{
		"error": gin.H{
			"message": message,
			"status":  code,
		},
	})
}

func (h *GeminiUpstreamFileAPIHandler) bindSession(uploadID, authID string) {
	if strings.TrimSpace(uploadID) == "" || strings.TrimSpace(authID) == "" {
		return
	}
	h.mu.Lock()
	if h.sessions == nil {
		h.sessions = make(map[string]uploadSession)
	}
	h.sessions[uploadID] = uploadSession{authID: authID, expiresAt: time.Now().Add(2 * time.Hour)}
	h.mu.Unlock()
}

func (h *GeminiUpstreamFileAPIHandler) sessionAuthID(uploadID string) (string, bool) {
	uploadID = strings.TrimSpace(uploadID)
	if uploadID == "" {
		return "", false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessions == nil {
		return "", false
	}
	s, ok := h.sessions[uploadID]
	if !ok || s.authID == "" || time.Now().After(s.expiresAt) {
		delete(h.sessions, uploadID)
		return "", false
	}
	return s.authID, true
}

func (h *GeminiUpstreamFileAPIHandler) dropSession(uploadID string) {
	uploadID = strings.TrimSpace(uploadID)
	if uploadID == "" {
		return
	}
	h.mu.Lock()
	if h.sessions != nil {
		delete(h.sessions, uploadID)
	}
	h.mu.Unlock()
}
