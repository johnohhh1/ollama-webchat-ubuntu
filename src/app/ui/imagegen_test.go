//go:build windows || darwin || linux

package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fakeImageRunner(t *testing.T, genBody string) *imageRunner {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			fmt.Fprint(w, `{"x/z-image-turbo": {"source": "a/b"}, "x/flux2-klein": {"source": "c/d"}}`)
		case "/api/generate":
			io.WriteString(w, genBody)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &imageRunner{base: srv.URL, client: srv.Client()}
}

func TestImageRunnerModels(t *testing.T) {
	r := fakeImageRunner(t, "")
	got := r.Models(context.Background())
	if strings.Join(got, ",") != "x/flux2-klein,x/z-image-turbo" {
		t.Fatalf("models = %v", got)
	}
	if !r.IsImageModel(context.Background(), "x/z-image-turbo:latest") {
		t.Fatal("expected x/z-image-turbo:latest to be an image model")
	}
	if r.IsImageModel(context.Background(), "llama3") {
		t.Fatal("llama3 is not an image model")
	}
}

func TestImageRunnerDown(t *testing.T) {
	r := &imageRunner{base: "http://127.0.0.1:1", client: http.DefaultClient}
	if got := r.Models(context.Background()); got != nil {
		t.Fatalf("expected no models when runner is down, got %v", got)
	}
}

func TestImageRunnerGenerateLargeLine(t *testing.T) {
	big := strings.Repeat("A", 3<<20) // larger than bufio.Scanner's default limit
	body := `{"done":false,"completed":1,"total":2}` + "\n" +
		`{"done":true,"image":"` + big + `"}` + "\n"
	r := fakeImageRunner(t, body)

	var progress, images int
	err := r.Generate(context.Background(), imageGenerateRequest{Model: "x/z-image-turbo"}, func(res imageGenerateResponse) error {
		if res.Done {
			if len(res.Image) != len(big) {
				t.Fatalf("image length = %d", len(res.Image))
			}
			images++
		} else {
			progress++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if progress != 1 || images != 1 {
		t.Fatalf("progress=%d images=%d", progress, images)
	}
}

func TestImageRunnerGenerateError(t *testing.T) {
	r := fakeImageRunner(t, `{"error":"CUDA out of memory"}`+"\n")
	err := r.Generate(context.Background(), imageGenerateRequest{Model: "x/z-image-turbo"}, func(imageGenerateResponse) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "CUDA out of memory") {
		t.Fatalf("err = %v", err)
	}
}

func TestWithImageModelsShow(t *testing.T) {
	saved := imgRunner
	imgRunner = fakeImageRunner(t, "")
	t.Cleanup(func() { imgRunner = saved })

	var proxied string
	proxy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		proxied = string(b)
	})
	h := (&Server{}).withImageModelsShow(proxy)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/show", strings.NewReader(`{"model":"x/z-image-turbo"}`)))
	var resp struct{ Capabilities []string }
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || len(resp.Capabilities) != 1 || resp.Capabilities[0] != "image" {
		t.Fatalf("image model show = %s (%v)", rec.Body.String(), err)
	}
	if proxied != "" {
		t.Fatal("image model request should not reach the Ollama proxy")
	}

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/show", strings.NewReader(`{"model":"llama3"}`)))
	if proxied != `{"model":"llama3"}` {
		t.Fatalf("non-image request body not passed through intact: %q", proxied)
	}
}
