//go:build windows || darwin || linux

package ui

// Image generation through an optional local runner (src/imagegen in the
// Linux port). Upstream Ollama removed its experimental image-generation
// API, so the app talks to the runner directly instead of going through
// the Ollama server. When the runner isn't running, nothing here is
// visible: no image models are listed and chats behave as usual.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ollama/ollama/api"
	"github.com/ollama/ollama/app/store"
	"github.com/ollama/ollama/app/ui/responses"
	"github.com/ollama/ollama/types/model"
)

const (
	EventImageProgress Event = "image_progress"
	EventImage         Event = "image"

	imageModelsTTL = 5 * time.Second
)

type imageRunner struct {
	base   string
	client *http.Client

	mu      sync.Mutex
	models  []string
	fetched time.Time
}

var imgRunner = newImageRunner()

func newImageRunner() *imageRunner {
	base := os.Getenv("OLLAMA_IMAGEGEN_HOST")
	if base == "" {
		base = "http://127.0.0.1:11435"
	}
	return &imageRunner{base: strings.TrimRight(base, "/"), client: &http.Client{}}
}

// Models returns the runner's model names, cached briefly. It returns nil
// when the runner is unreachable.
func (r *imageRunner) Models(ctx context.Context) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if time.Since(r.fetched) < imageModelsTTL {
		return r.models
	}

	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	r.models = nil
	r.fetched = time.Now()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.base+"/models", nil)
	if err != nil {
		return nil
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var cfg map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		return nil
	}
	for name := range cfg {
		r.models = append(r.models, name)
	}
	sort.Strings(r.models)
	return r.models
}

func (r *imageRunner) IsImageModel(ctx context.Context, name string) bool {
	name = strings.TrimSuffix(name, ":latest")
	for _, m := range r.Models(ctx) {
		if m == name {
			return true
		}
	}
	return false
}

type imageGenerateRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Width  *int32 `json:"width,omitempty"`
	Height *int32 `json:"height,omitempty"`
	Steps  *int32 `json:"steps,omitempty"`
}

type imageGenerateResponse struct {
	Done      bool   `json:"done"`
	Completed int64  `json:"completed"`
	Total     int64  `json:"total"`
	Image     string `json:"image"`
	Error     string `json:"error"`
}

// Generate streams NDJSON progress from the runner to fn.
func (r *imageRunner) Generate(ctx context.Context, in imageGenerateRequest, fn func(imageGenerateResponse) error) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.base+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("image runner unreachable at %s: %w", r.base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("image runner: %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}

	// Lines carry a whole base64 PNG, so don't use bufio.Scanner's 64K limit.
	rd := bufio.NewReader(resp.Body)
	for {
		line, err := rd.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var res imageGenerateResponse
			if jerr := json.Unmarshal(line, &res); jerr != nil {
				return fmt.Errorf("image runner: bad response: %w", jerr)
			}
			if res.Error != "" {
				return errors.New(res.Error)
			}
			if ferr := fn(res); ferr != nil {
				return ferr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// imageChat answers a chat turn with an image model: it asks the runner
// for an image, streams progress to the UI, and stores the PNG as an
// attachment on the assistant message.
func (s *Server) imageChat(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, chat *store.Chat, req responses.ChatRequest) error {
	send := func(ev any) {
		json.NewEncoder(w).Encode(ev) //nolint:errcheck
		flusher.Flush()
	}

	chat.Messages = append(chat.Messages, store.NewMessage("assistant", "", &store.MessageOptions{Model: req.Model}))
	if err := s.Store.SetChat(*chat); err != nil {
		return err
	}

	gotImage := false
	err := imgRunner.Generate(ctx, imageGenerateRequest{
		Model:  strings.TrimSuffix(req.Model, ":latest"),
		Prompt: req.Prompt,
		Width:  req.Width,
		Height: req.Height,
		Steps:  req.Steps,
	}, func(res imageGenerateResponse) error {
		if !res.Done {
			if res.Total > 0 {
				completed, total := res.Completed, res.Total
				send(responses.ChatEvent{EventName: string(EventImageProgress), Completed: &completed, Total: &total})
			}
			return nil
		}
		if res.Image == "" {
			return nil
		}

		imageBytes, err := base64.StdEncoding.DecodeString(res.Image)
		if err != nil {
			return fmt.Errorf("failed to decode image: %w", err)
		}
		if _, err := s.Store.ImgToFile(chat.ID, imageBytes, "generated.png", "image/png"); err != nil {
			s.log().Error("failed to save generated image to disk", "error", err)
		}

		last := &chat.Messages[len(chat.Messages)-1]
		last.Attachments = append(last.Attachments, store.File{Filename: "generated.png", Data: imageBytes})
		last.UpdatedAt = time.Now()
		if err := s.Store.UpdateLastMessage(chat.ID, *last); err != nil {
			s.log().Error("failed to store generated image", "error", err)
		}

		image := res.Image
		send(responses.ChatEvent{EventName: string(EventImage), Image: &image})
		gotImage = true
		return nil
	})
	if err == nil && !gotImage {
		err = errors.New("image runner finished without returning an image")
	}
	if err != nil {
		s.log().Error("image generation error", "error", err, "model", req.Model)
		send(s.getError(err))
		return nil
	}

	send(responses.ChatEvent{EventName: "done"})
	chat.Messages[len(chat.Messages)-1].Stream = false
	return s.Store.SetChat(*chat)
}

// writeImageJSON writes a response the wrappers below build themselves
// (instead of proxying), with the same dev-mode CORS headers the rest of
// the UI server uses.
func writeImageJSON(w http.ResponseWriter, v any) {
	if CORS() {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, User-Agent, Accept, X-Requested-With")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

// withImageModelsList wraps the /api/tags proxy so the model picker also
// lists the runner's image models.
func (s *Server) withImageModelsList(proxy http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		names := imgRunner.Models(r.Context())
		if len(names) == 0 {
			proxy.ServeHTTP(w, r)
			return
		}
		list, err := s.inferenceClient().List(r.Context())
		if err != nil {
			proxy.ServeHTTP(w, r)
			return
		}
		for _, name := range names {
			list.Models = append(list.Models, api.ListModelResponse{
				Name:         name + ":latest",
				Model:        name + ":latest",
				Details:      api.ModelDetails{Family: "image", Format: "diffusers"},
				Capabilities: []model.Capability{model.CapabilityImage},
				ModifiedAt:   time.Now(),
			})
		}
		writeImageJSON(w, list)
	})
}

// withImageModelsShow wraps the /api/show proxy so image models report the
// "image" capability, which switches the chat form into image mode.
func (s *Server) withImageModelsShow(proxy http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		r.Body.Close()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var sr api.ShowRequest
		if json.Unmarshal(body, &sr) == nil {
			name := sr.Model
			if name == "" {
				name = sr.Name
			}
			if name != "" && imgRunner.IsImageModel(r.Context(), name) {
				writeImageJSON(w, api.ShowResponse{
					Details:      api.ModelDetails{Family: "image", Format: "diffusers"},
					Capabilities: []model.Capability{model.CapabilityImage},
				})
				return
			}
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		proxy.ServeHTTP(w, r)
	})
}
