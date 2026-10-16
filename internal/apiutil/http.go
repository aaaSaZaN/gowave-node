package apiutil

import (
	"compress/gzip"
	"compress/zlib"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

const MaxBody = 1000 * 1024 * 1024

func Decode(r *http.Request, v interface{}) error {
	d := json.NewDecoder(r.Body)
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(new(interface{})); err != io.EOF {
		return errors.New("expected one JSON object")
	}
	if x, ok := v.(interface{ Validate() error }); ok {
		return x.Validate()
	}
	return nil
}

func Error(w http.ResponseWriter, r *http.Request, status int, code string, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"timestamp": time.Now().UTC().Format(time.RFC3339Nano), "path": r.URL.Path, "message": err.Error(), "errorCode": code})
}

func Decompress(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reader io.Reader = http.MaxBytesReader(w, r.Body, MaxBody)
		var closeDecoder func()
		var err error
		switch strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))) {
		case "", "identity":
		case "gzip":
			var d *gzip.Reader
			d, err = gzip.NewReader(reader)
			if err == nil {
				reader = d
				closeDecoder = func() { _ = d.Close() }
			}
		case "deflate":
			var d io.ReadCloser
			d, err = zlib.NewReader(reader)
			if err == nil {
				reader = d
				closeDecoder = func() { _ = d.Close() }
			}
		case "br":
			reader = brotli.NewReader(reader)
		case "zstd":
			var d *zstd.Decoder
			d, err = zstd.NewReader(reader, zstd.WithDecoderMaxMemory(MaxBody), zstd.WithDecoderConcurrency(1))
			if err == nil {
				reader = d
				closeDecoder = d.Close
			}
		default:
			Error(w, r, http.StatusUnsupportedMediaType, "E000", errors.New("unsupported content encoding"))
			return
		}
		if err != nil {
			Error(w, r, 400, "E000", errors.New("invalid compressed body"))
			return
		}
		if closeDecoder != nil {
			defer closeDecoder()
		}
		original := r.Body
		defer original.Close()
		r.Body = http.MaxBytesReader(w, io.NopCloser(reader), MaxBody)
		r.ContentLength = -1
		r.Header.Del("Content-Encoding")
		next.ServeHTTP(w, r)
	})
}
