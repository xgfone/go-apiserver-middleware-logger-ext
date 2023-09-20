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
//	import _ "github.com/xgfone/go-apiserver-middleware-logger-ext"
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

	"github.com/xgfone/gconf/v6"
	"github.com/xgfone/go-apiserver/helper"
	"github.com/xgfone/go-apiserver/http/header"
	"github.com/xgfone/go-apiserver/http/middleware"
	"github.com/xgfone/go-apiserver/http/middleware/logger"
	"github.com/xgfone/go-apiserver/http/router"
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
		header.MIMEApplicationJSON, header.MIMEApplicationForm,
	}, "The content types of the request or response body to log.")
)

var bufpool = sync.Pool{New: func() interface{} { return bytes.NewBuffer(make([]byte, 0, 512)) }}

func getbuffer() *bytes.Buffer  { return bufpool.Get().(*bytes.Buffer) }
func putbuffer(b *bytes.Buffer) { b.Reset(); bufpool.Put(b) }

func init() {
	logger.Collect = collect
	logger.Enabled = enabled

	middlewares := make(middleware.Middlewares, 0, len(middleware.DefaultMiddlewares)+2)
	middlewares = append(middlewares, middleware.MiddlewareFunc(wrapRequestBody))
	middlewares = append(middlewares, middleware.MiddlewareFunc(wrapResponseBody))
	middlewares = append(middlewares, middleware.DefaultMiddlewares)
	middleware.DefaultMiddlewares = middlewares
	router.DefaultRouter.Middlewares.Reset(middlewares...)
}

func enabled(req *http.Request) bool { return req.URL.Path != "/" }
func collect(w http.ResponseWriter, r *http.Request, appendAttr func(...slog.Attr)) {
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
		if ct := header.ContentType(w.Header()); shouldlogbody(ct, _len) {
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
	return slog.String(key, helper.String(data))
}

/// ----------------------------------------------------------------------- ///

func wrapRequestBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if logReqBody.Get() {
			reqbody := reqbody{ct: header.ContentType(r.Header)}
			if slices.Contains(logBodyTypes.Get(), reqbody.ct) {
				buf := getbuffer()
				defer putbuffer(buf)

				_, err := io.CopyBuffer(buf, r.Body, make([]byte, 512))
				if err != nil {
					slog.Error("fail to read the request body", "raddr", r.RemoteAddr,
						"method", r.Method, "path", r.RequestURI, "err", err)
				}

				reqbody.data = buf.Bytes()
				r.Body = io.NopCloser(buf)

				r = r.WithContext(context.WithValue(r.Context(), reqbodykey, reqbody))
			}
		}

		next.ServeHTTP(w, r)
	})
}

var reqbodykey = contextkey{key: "reqbodybuf"}

type contextkey struct{ key string }
type reqbody struct {
	data []byte
	ct   string
}

/// ----------------------------------------------------------------------- ///

func wrapResponseBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !logRespBody.Get() {
			next.ServeHTTP(w, r)
			return
		}

		buf := getbuffer()
		next.ServeHTTP(newResponseWriter(w, buf), r)
		putbuffer(buf)
	})
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
