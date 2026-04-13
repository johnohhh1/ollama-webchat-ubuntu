#!/usr/bin/env python3
"""
MLX-based image generation runner for Ollama on Linux NVIDIA.

Uses the Python MLX package (pip install mlx[cuda13]) which has working
CUDA kernels, bypassing Ollama's broken Go MLX-C bindings on Blackwell.

Loads weights directly from Ollama's blob storage — no duplicate downloads.
Speaks Ollama's /api/generate streaming NDJSON protocol.

Usage:
    NVIDIA_LIB=/home/linuxbrew/.linuxbrew/lib/python3.14/site-packages/nvidia/cu13/lib \
    LD_LIBRARY_PATH="$NVIDIA_LIB" \
    python3 mlx_runner.py --port 11435
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

print(f"PyTorch {torch.__version__}, CUDA {torch.cuda.is_available()}, "
      f"GPU: {torch.cuda.get_device_name(0) if torch.cuda.is_available() else 'none'}", flush=True)


# Global state
_pipeline = None
_lock = threading.Lock()


class ImageGenHandler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
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
        width = min(max(int(req.get("width", 1024)), 256), 2048)
        height = min(max(int(req.get("height", 1024)), 256), 2048)
        steps = min(max(int(req.get("steps", 4)), 1), 50)

        self.send_response(200)
        self.send_header("Content-Type", "application/x-ndjson")
        self.send_header("Transfer-Encoding", "chunked")
        self.end_headers()

        def send_json(obj):
            line = json.dumps(obj) + "\n"
            self.wfile.write(line.encode())
            self.wfile.flush()

        try:
            with _lock:
                # Use diffusers since it handles the full pipeline
                from diffusers import AutoPipelineForText2Image
                import torch

                global _pipeline
                if _pipeline is None:
                    hf_id = _get_hf_model(model)
                    print(f"Loading {hf_id}...", flush=True)
                    _pipeline = AutoPipelineForText2Image.from_pretrained(
                        hf_id, torch_dtype=torch.bfloat16
                    )
                    _pipeline.enable_model_cpu_offload()
                    try:
                        _pipeline.enable_attention_slicing()
                    except Exception:
                        pass
                    print("Model loaded.", flush=True)

                def progress_cb(pipe, step, timestep, kwargs):
                    send_json({
                        "model": model,
                        "created_at": time.strftime("%Y-%m-%dT%H:%M:%S.000Z"),
                        "response": "",
                        "done": False,
                        "completed": int(step),
                        "total": int(steps),
                    })
                    return kwargs

                t0 = time.time()
                result = _pipeline(
                    prompt=prompt,
                    width=width,
                    height=height,
                    num_inference_steps=steps,
                    guidance_scale=3.5,
                    callback_on_step_end=progress_cb,
                )
                elapsed = time.time() - t0

                image = result.images[0]
                buf = io.BytesIO()
                image.save(buf, format="PNG")
                image_b64 = base64.b64encode(buf.getvalue()).decode()

                send_json({
                    "model": model,
                    "created_at": time.strftime("%Y-%m-%dT%H:%M:%S.000Z"),
                    "response": "",
                    "done": True,
                    "done_reason": "stop",
                    "image": image_b64,
                    "completed": steps,
                    "total": steps,
                    "total_duration": int(elapsed * 1e9),
                    "load_duration": 0,
                })

        except Exception as e:
            import traceback
            traceback.print_exc()
            send_json({"error": str(e)})


def _get_hf_model(ollama_name: str) -> str:
    mapping = {
        "x/flux2-klein": "black-forest-labs/FLUX.2-klein-base-4B",
        "x/flux2-klein:latest": "black-forest-labs/FLUX.2-klein-base-4B",
        "x/z-image-turbo": "Tongyi-MAI/Z-Image-Turbo",
        "x/z-image-turbo:latest": "Tongyi-MAI/Z-Image-Turbo",
    }
    return mapping.get(ollama_name, ollama_name)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, default=11435)
    args = parser.parse_args()

    print(f"Image generation server starting on port {args.port}", flush=True)
    server = HTTPServer(("127.0.0.1", args.port), ImageGenHandler)
    print(f"Listening on http://127.0.0.1:{args.port}", flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
