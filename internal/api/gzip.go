package api

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

const (
	// gzipMinSize is the smallest body worth compressing: below it the gzip
	// header and trailer (~20 bytes) make the response larger, not smaller.
	gzipMinSize = 256
	// maxPooledBuffer keeps unusually large buffers out of the pool so one
	// big response does not pin memory forever.
	maxPooledBuffer = 1 << 20
)

var (
	bufferPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}
	gzipPool   = sync.Pool{New: func() any { return gzip.NewWriter(io.Discard) }}
)

func getBuffer() *bytes.Buffer {
	buf := bufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	return buf
}

func putBuffer(buf *bytes.Buffer) {
	if buf.Cap() <= maxPooledBuffer {
		bufferPool.Put(buf)
	}
}

type bufferedResponse struct {
	header      http.Header
	body        *bytes.Buffer
	status      int
	wroteHeader bool
}

func newBufferedResponse() *bufferedResponse {
	return &bufferedResponse{header: make(http.Header), body: getBuffer()}
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
		defer putBuffer(buffered.body)
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
			len(body) >= gzipMinSize &&
			buffered.header.Get("Content-Encoding") == "" &&
			acceptsGzip(r) &&
			compressible

		if shouldCompress {
			compressed := getBuffer()
			defer putBuffer(compressed)
			writer := gzipPool.Get().(*gzip.Writer)
			writer.Reset(compressed)
			_, _ = writer.Write(body)
			_ = writer.Close()
			gzipPool.Put(writer)
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
