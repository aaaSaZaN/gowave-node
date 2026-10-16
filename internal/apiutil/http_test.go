package apiutil

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCompressedJSON(t *testing.T) {
	raw := []byte(`{"tag":"test","reset":true}`)
	for _, encoding := range []string{"identity", "gzip", "deflate", "br", "zstd"} {
		t.Run(encoding, func(t *testing.T) {
			var buffer bytes.Buffer
			var writer io.WriteCloser
			switch encoding {
			case "gzip":
				writer = gzip.NewWriter(&buffer)
			case "deflate":
				writer = zlib.NewWriter(&buffer)
			case "br":
				writer = brotli.NewWriter(&buffer)
			case "zstd":
				writer, _ = zstd.NewWriter(&buffer)
			default:
				writer = nopWriter{&buffer}
			}
			writer.Write(raw)
			writer.Close()
			req := httptest.NewRequest("POST", "/", &buffer)
			req.Header.Set("Content-Encoding", encoding)
			response := httptest.NewRecorder()
			Decompress(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Tag   string
					Reset bool
				}
				if err := Decode(r, &body); err != nil {
					t.Fatal(err)
				}
				if body.Tag != "test" || !body.Reset {
					t.Fatal(body)
				}
				w.WriteHeader(204)
			})).ServeHTTP(response, req)
			if response.Code != 204 {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
		})
	}
}

type nopWriter struct{ io.Writer }

func (nopWriter) Close() error { return nil }
func TestRejectTrailingJSON(t *testing.T) {
	req := httptest.NewRequest("POST", "/", bytes.NewBufferString(`{} {}`))
	if Decode(req, &map[string]interface{}{}) == nil {
		t.Fatal("accepted two objects")
	}
}
