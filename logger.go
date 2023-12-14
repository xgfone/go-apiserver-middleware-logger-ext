// Copyright 2023 xgfone
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package loggerext provides an extension of
// "github.com/xgfone/go-apiserver/http/middleware/logger"
// to support to log the request and response header and body.
//
// # Usage
//
//	import (
//		loggerext "github.com/xgfone/go-apiserver-middleware-logger-ext"
//		"github.com/xgfone/go-apiserver/http/router"
//	)
//
//	router.DefaultRouter.Middlewares.InsertFunc(loggerext.WrapHandler)
package loggerext

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"unsafe"

	"github.com/xgfone/gconf/v6"
	"github.com/xgfone/go-rawjson"
)

var (
	group          = gconf.Group("log")
	logQuery       = group.NewBool("query", false, "If true, log the request query.")
	logReqBody     = group.NewBool("reqbody", false, "If true, log the request body.")
	logRespBody    = group.NewBool("respbody", false, "If true, log the response body.")
	logReqHeaders  = group.NewBool("reqheaders", false, "If true, log the request headers.")
	logRespHeaders = group.NewBool("respheaders", false, "If true, log the response headers.")

	logBodyMaxLen = group.NewInt("bodymaxlen", 2048,
		"The maximum length of the request or response body to log.")
	logBodyTypes = group.NewStringSlice("bodytypes", []string{
		"application/json", "application/x-www-form-urlencoded",
	}, "The content types of the request or response body to log.")
)

var bufpool = sync.Pool{New: func() interface{} { return bytes.NewBuffer(make([]byte, 0, 512)) }}

func getbuffer() *bytes.Buffer  { return bufpool.Get().(*bytes.Buffer) }
func putbuffer(b *bytes.Buffer) { b.Reset(); bufpool.Put(b) }

type ctxkeytype int8

var logrespkey = ctxkeytype(0)

func logRespFromContext(ctx context.Context) (log, ok bool) {
	if v := ctx.Value(logrespkey); v != nil {
		return v.(bool), true
	}
	return
}

// DisableLogRespBody returns a new context to set a flag to indicate
// not to log the response body.
//
// If not set, use the default policy.
func DisableLogRespBody(ctx context.Context) context.Context {
	return context.WithValue(ctx, logrespkey, false)
}

// WrapHandler wraps a http handler and returns a new,
// which will replace the request and response writer,
// so must be used before the logger middleware.
func WrapHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w, r = WrapReqRespBody(w, r)
		defer Release(w, r)
		next.ServeHTTP(w, r)
	})
}

// Enabled reports whether to log the request.
func Enabled(req *http.Request) bool { return req.URL.Path != "/" }

// Collect collects the key-value log information and appends them by appendAttr.
func Collect(w http.ResponseWriter, r *http.Request, appendAttr func(...slog.Attr)) {
	if logQuery.Get() {
		appendAttr(slog.String("query", r.URL.RawQuery))
	}

	if logReqHeaders.Get() {
		appendAttr(slog.Any("reqheaders", r.Header))
	}

	if logRespHeaders.Get() {
		appendAttr(slog.Any("respheaders", w.Header()))
	}

	if reqbody, ok := r.Context().Value(reqbodykey).(reqbody); ok {
		appendAttr(slog.Int("reqbodylen", len(reqbody.data)))
		if shouldlogbody(reqbody.ct, len(reqbody.data)) {
			appendAttr(getbodyattr(reqbody.data, "reqbody", reqbody.ct))
		}
	}

	if rw := getResponseWriter(w); rw != nil {
		_len := rw.buf.Len()
		appendAttr(slog.Int("respbodylen", _len))
		if ct := getContentType(w.Header()); shouldlogbody(ct, _len) {
			appendAttr(getbodyattr(rw.buf.Bytes(), "respbody", ct))
		}
	}
}

func shouldlogbody(ct string, datalen int) bool {
	if maxlen := logBodyMaxLen.Get(); maxlen > 0 && datalen > maxlen {
		return false
	}

	if !slices.Contains(logBodyTypes.Get(), ct) {
		return false
	}

	return true
}

func getbodyattr(data []byte, key, ct string) slog.Attr {
	if strings.HasSuffix(ct, "json") {
		return slog.Any(key, rawjson.Bytes(data))
	}
	return slog.String(key, unsafe.String(unsafe.SliceData(data), len(data)))
}

func getContentType(header http.Header) (mime string) {
	mime = header.Get("Content-Type")
	if index := strings.IndexByte(mime, ';'); index > -1 {
		mime = strings.TrimSpace(mime[:index])
	}
	return
}

/// ----------------------------------------------------------------------- ///

// WrapReqRespBody wraps the http request and response writer, and returns the new,
// which is used by the http middleware, such as WrapHandler.
//
// NOTICE: Release should be called after handling the request.
func WrapReqRespBody(w http.ResponseWriter, r *http.Request) (http.ResponseWriter, *http.Request) {
	w, r = wrapRequestBody(w, r)
	w, r = wrapResponseBody(w, r)
	return w, r
}

// Release tries to release the buffer into the pool.
func Release(w http.ResponseWriter, r *http.Request) {
	if reqbody, ok := r.Context().Value(reqbodykey).(reqbody); ok {
		putbuffer(reqbody.buf)
	}
	if rw := getResponseWriter(w); rw != nil {
		putbuffer(rw.buf)
	}
}

/// ----------------------------------------------------------------------- ///

func wrapRequestBody(w http.ResponseWriter, r *http.Request) (http.ResponseWriter, *http.Request) {
	if !logReqBody.Get() {
		return w, r
	}

	reqbody := reqbody{ct: getContentType(r.Header)}
	if slices.Contains(logBodyTypes.Get(), reqbody.ct) {
		reqbody.buf = getbuffer()
		_, err := io.CopyBuffer(reqbody.buf, r.Body, make([]byte, 512))
		if err != nil {
			slog.Error("fail to read the request body", "raddr", r.RemoteAddr,
				"method", r.Method, "path", r.RequestURI, "err", err)
		}

		reqbody.data = reqbody.buf.Bytes()
		r.Body = io.NopCloser(reqbody.buf)

		r = r.WithContext(context.WithValue(r.Context(), reqbodykey, reqbody))
	}

	return w, r
}

var (
	reqbodykey  = contextkey{key: "reqbodykey"}
	respbodykey = contextkey{key: "respbodykey"}
)

type contextkey struct{ key string }
type reqbody struct {
	data []byte
	buf  *bytes.Buffer
	ct   string
}

/// ----------------------------------------------------------------------- ///

func wrapResponseBody(w http.ResponseWriter, r *http.Request) (http.ResponseWriter, *http.Request) {
	if !logRespBody.Get() {
		return w, r
	}

	if log, ok := logRespFromContext(r.Context()); ok && !log {
		return w, r
	}

	buf := getbuffer()
	w = newResponseWriter(w, buf)
	r = r.WithContext(context.WithValue(r.Context(), respbodykey, w))

	return w, r
}

func getResponseWriter(w http.ResponseWriter) *responseWriter {
	for {
		switch v := w.(type) {
		case *responseWriter:
			return v

		case interface{ Unwrap() http.ResponseWriter }:
			w = v.Unwrap()

		default:
			return nil
		}
	}
}

type responseWriter struct {
	http.ResponseWriter
	buf *bytes.Buffer
}

func newResponseWriter(w http.ResponseWriter, buf *bytes.Buffer) *responseWriter {
	return &responseWriter{ResponseWriter: w, buf: buf}
}

func (r *responseWriter) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (r *responseWriter) Write(p []byte) (n int, err error) {
	if n, err = r.ResponseWriter.Write(p); n > 0 {
		r.buf.Write(p[:n])
	}
	return
}

func (r *responseWriter) WriteString(s string) (n int, err error) {
	if n, err = io.WriteString(r.ResponseWriter, s); n > 0 {
		r.buf.WriteString(s[:n])
	}
	return
}
