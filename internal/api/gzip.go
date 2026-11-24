package api

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"strconv"
	"strings"
)

type bufferedResponse struct {
	header      http.Header
	body        bytes.Buffer
	status      int
	wroteHeader bool
}

func newBufferedResponse() *bufferedResponse {
	return &bufferedResponse{header: make(http.Header)}
}

func (b *bufferedResponse) Header() http.Header {
	return b.header
}

func (b *bufferedResponse) WriteHeader(status int) {
	if b.wroteHeader {
		return
	}
	b.status = status
	b.wroteHeader = true
}

func (b *bufferedResponse) Write(data []byte) (int, error) {
	if !b.wroteHeader {
		b.WriteHeader(http.StatusOK)
	}
	return b.body.Write(data)
}

func GzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buffered := newBufferedResponse()
		next.ServeHTTP(buffered, r)
		status := buffered.status
		if status == 0 {
			status = http.StatusOK
		}

		body := buffered.body.Bytes()
		contentType := buffered.header.Get("Content-Type")
		compressible := isCompressible(contentType)
		if compressible {
			addVaryHeader(buffered.header, "Accept-Encoding")
		}

		shouldCompress := status >= 200 && status < 300 &&
			status != http.StatusNoContent &&
			status != http.StatusNotModified &&
			r.Method != http.MethodHead &&
			len(body) > 0 &&
			buffered.header.Get("Content-Encoding") == "" &&
			acceptsGzip(r) &&
			compressible

		if shouldCompress {
			var compressed bytes.Buffer
			writer := gzip.NewWriter(&compressed)
			_, _ = writer.Write(body)
			_ = writer.Close()
			body = compressed.Bytes()
			buffered.header.Set("Content-Encoding", "gzip")
			buffered.header.Set("Content-Length", strconv.Itoa(len(body)))
			if etag := buffered.header.Get("ETag"); etag != "" {
				buffered.header.Set("ETag", weakETag(etag))
			}
		}

		switch {
		case status == http.StatusNotModified || status == http.StatusNoContent:
			buffered.header.Del("Content-Length")
			body = nil
		case r.Method == http.MethodHead:
			body = nil
		default:
			if status >= 200 && status < 300 {
				buffered.header.Set("Content-Length", strconv.Itoa(len(body)))
			}
		}

		copyHeader(w.Header(), buffered.header)
		w.WriteHeader(status)
		if len(body) > 0 {
			_, _ = w.Write(body)
		}
	})
}

func copyHeader(dst, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func isCompressible(contentType string) bool {
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	switch mediaType {
	case "application/json", "text/markdown", "text/plain":
		return true
	default:
		return false
	}
}

func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(fields[0]), "gzip") {
			continue
		}
		quality := 1.0
		for _, parameter := range fields[1:] {
			parameter = strings.TrimSpace(parameter)
			if !strings.HasPrefix(parameter, "q=") {
				continue
			}
			if parsed, err := strconv.ParseFloat(strings.TrimPrefix(parameter, "q="), 64); err == nil {
				quality = parsed
			}
		}
		return quality > 0
	}
	return false
}
