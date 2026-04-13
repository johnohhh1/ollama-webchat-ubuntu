#!/usr/bin/env python3
"""
Ollama-compatible image generation server using diffusers + PyTorch CUDA.

Replaces Ollama's broken MLX CUDA runner for Linux NVIDIA GPUs.
Speaks the same streaming NDJSON protocol that the desktop app expects.

Usage:
    python3 server.py --port 11435 --model x/flux2-klein
"""

import argparse
import base64
import io
import json
import sys
import threading
import time
from http.server import HTTPServer, BaseHTTPRequestHandler

import torch
from diffusers import AutoPipelineForText2Image


# Global state
_pipeline = None
_pipeline_lock = threading.Lock()
_model_name = None


def get_hf_model_id(ollama_model: str) -> str:
    """Map Ollama model names to HuggingFace model IDs."""
    mapping = {
        "x/flux2-klein": "black-forest-labs/FLUX.2-klein-base-4B",
        "x/flux2-klein:latest": "black-forest-labs/FLUX.2-klein-base-4B",
        "x/z-image-turbo": "Tongyi-MAI/Z-Image-Turbo",
        "x/z-image-turbo:latest": "Tongyi-MAI/Z-Image-Turbo",
    }
    return mapping.get(ollama_model, ollama_model)


def load_pipeline(model_name: str):
    """Load the diffusers pipeline for the given model."""
    global _pipeline, _model_name

    if _pipeline is not None and _model_name == model_name:
        return _pipeline

    hf_id = get_hf_model_id(model_name)
    print(f"Loading model {hf_id} ...", flush=True)
    start = time.time()

    pipe = AutoPipelineForText2Image.from_pretrained(
        hf_id,
        torch_dtype=torch.bfloat16,
    )
    pipe.to("cuda")
    try:
        pipe.enable_attention_slicing()
    except Exception:
        pass

    elapsed = time.time() - start
    print(f"Model loaded in {elapsed:.1f}s", flush=True)

    _pipeline = pipe
    _model_name = model_name
    return pipe


class ImageGenHandler(BaseHTTPRequestHandler):
    def log_message(self, format, *args):
        # Quiet logs
        pass

    def do_GET(self):
        if self.path == "/health":
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps({"status": "ok"}).encode())
            return

        self.send_error(404)

    def do_POST(self):
        if self.path == "/api/generate":
            self._handle_generate()
            return

        self.send_error(404)

    def _handle_generate(self):
        content_length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(content_length)
        req = json.loads(body)

        model = req.get("model", "x/flux2-klein")
        prompt = req.get("prompt", "")
        width = req.get("width", 1024)
        height = req.get("height", 1024)
        steps = req.get("steps", 4)

        # Clamp dimensions
        width = min(max(int(width), 256), 2048)
        height = min(max(int(height), 256), 2048)
        steps = min(max(int(steps), 1), 50)

        self.send_response(200)
        self.send_header("Content-Type", "application/x-ndjson")
        self.send_header("Transfer-Encoding", "chunked")
        self.end_headers()

        def send_json(obj):
            line = json.dumps(obj) + "\n"
            self.wfile.write(line.encode())
            self.wfile.flush()

        try:
            with _pipeline_lock:
                pipe = load_pipeline(model)

                # Send progress via callback
                def progress_callback(pipe_instance, step, timestep, kwargs):
                    send_json({
                        "model": model,
                        "created_at": time.strftime("%Y-%m-%dT%H:%M:%S.000Z"),
                        "response": "",
                        "done": False,
                        "completed": int(step),
                        "total": int(steps),
                    })
                    return kwargs

                gen_start = time.time()

                result = pipe(
                    prompt=prompt,
                    width=width,
                    height=height,
                    num_inference_steps=steps,
                    guidance_scale=3.5,
                    callback_on_step_end=progress_callback,
                )

                gen_elapsed = time.time() - gen_start

                # Encode image to base64 PNG
                image = result.images[0]
                buf = io.BytesIO()
                image.save(buf, format="PNG")
                image_b64 = base64.b64encode(buf.getvalue()).decode()

                # Send final response with image
                send_json({
                    "model": model,
                    "created_at": time.strftime("%Y-%m-%dT%H:%M:%S.000Z"),
                    "response": "",
                    "done": True,
                    "done_reason": "stop",
                    "image": image_b64,
                    "completed": steps,
                    "total": steps,
                    "total_duration": int(gen_elapsed * 1e9),
                    "load_duration": 0,
                })

        except Exception as e:
            send_json({
                "error": str(e),
            })


def main():
    parser = argparse.ArgumentParser(description="Image generation server")
    parser.add_argument("--port", type=int, default=11435)
    parser.add_argument("--model", type=str, default="x/flux2-klein")
    args = parser.parse_args()

    # Pre-load the model
    print(f"Starting image generation server on port {args.port}", flush=True)
    try:
        load_pipeline(args.model)
    except Exception as e:
        print(f"Warning: could not pre-load model: {e}", flush=True)

    server = HTTPServer(("127.0.0.1", args.port), ImageGenHandler)
    print(f"Listening on http://127.0.0.1:{args.port}", flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
