package main

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"net/http"
	"strings"
)

func (g *gzipResponseWriter) WriteHeader(statusCode int) {
	// 设置 Gzip 响应头
	g.ResponseWriter.Header().Set("Content-Encoding", "gzip")
	g.ResponseWriter.WriteHeader(statusCode)
}

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	// 写入 Gzip 压缩数据
	if g.gzipWriter != nil {
		return g.gzipWriter.Write(b)
	}
	return g.ResponseWriter.Write(b)
}

func (d *deflateResponseWriter) Write(b []byte) (int, error) {
	return d.Writer.Write(b)
}

func handlePreflight(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS, PUT, DELETE")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Access-Control-Max-Age", "86400") // 缓存 1 天
	w.WriteHeader(http.StatusNoContent)
}

// CompressionMiddleware 用于处理压缩
func CompressionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 检查是否为 WebSocket 握手请求
		if r.Header.Get("Upgrade") == "websocket" {
			next.ServeHTTP(w, r)
			return
		}

		// 检查请求头中支持的编码
		acceptEncoding := r.Header.Get("Accept-Encoding")
		if strings.Contains(acceptEncoding, "gzip") {
			// 使用 Gzip 压缩
			w.Header().Set("Content-Encoding", "gzip")
			gz := gzip.NewWriter(w)
			defer gz.Close()
			gw := &gzipResponseWriter{
				Writer:         gz,
				ResponseWriter: w,
				gzipWriter:     gz,
			}
			next.ServeHTTP(gw, r)
		} else if strings.Contains(acceptEncoding, "deflate") {
			// 使用 Deflate 压缩
			var buf bytes.Buffer
			writer, err := flate.NewWriter(&buf, flate.BestCompression)
			if err != nil {
				http.Error(w, "Failed to create flate writer", http.StatusInternalServerError)
				return
			}
			defer writer.Close()

			w.Header().Set("Content-Encoding", "deflate")
			dw := &deflateResponseWriter{Writer: writer, ResponseWriter: w}
			next.ServeHTTP(dw, r)

			writer.Close()
			w.Write(buf.Bytes())
		} else {
			// 不支持压缩，直接处理请求
			next.ServeHTTP(w, r)
		}
	})
}
