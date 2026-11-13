#!/usr/bin/env python3
"""
Image generation runner for the Ollama Linux desktop app.

Uses PyTorch + diffusers on CUDA. The desktop app talks to it directly
(upstream Ollama removed its experimental image-generation API), so it is
optional: when it isn't running, the app simply shows no image models.

Supports:
  - HuggingFace diffusers models (default: x/z-image-turbo, x/flux2-klein)
  - Local ComfyUI-style .safetensors checkpoints (type "checkpoint")

Models are configured in models.json, created with defaults on first run at
~/.config/ollama-imagegen/models.json (override with --models). Each entry:

    "x/name": {
        "source": "org/repo" | "/path/to/model.safetensors",
        "type": "huggingface" | "checkpoint",
        "guidance": 0.0,        # optional, default 3.5
        "steps": 8,             # optional, used when the request has none
        "offload": "model",     # optional: "model" (fast) or "sequential"
                                # (fits in less VRAM, slower). "model" falls
                                # back to "sequential" on CUDA OOM.
        "base": "Qwen/Qwen-Image"   # checkpoint only
    }

API (Ollama-style streaming NDJSON):
    GET  /health         -> {"status": "ok"}
    GET  /models         -> contents of models.json
    POST /api/generate   {"model", "prompt", "width"?, "height"?, "steps"?}

Usage:
    python3 imagegen_server.py [--port 11435] [--models PATH]
"""

import argparse
import base64
import io
import json
import os
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

import torch

print(f"PyTorch {torch.__version__}, CUDA {torch.cuda.is_available()}, "
      f"GPU: {torch.cuda.get_device_name(0) if torch.cuda.is_available() else 'none'}", flush=True)

CONFIG_DIR = Path(os.environ.get("XDG_CONFIG_HOME", Path.home() / ".config")) / "ollama-imagegen"
MODELS_JSON = CONFIG_DIR / "models.json"

DEFAULT_GUIDANCE = 3.5

DEFAULT_MODELS = {
    "x/z-image-turbo": {
        "source": "Tongyi-MAI/Z-Image-Turbo",
        "type": "huggingface",
        "guidance": 0.0,
        "steps": 8,
    },
    "x/flux2-klein": {
        "source": "black-forest-labs/FLUX.2-klein-base-4B",
        "type": "huggingface",
    },
}

# Only one pipeline is kept in VRAM at a time.
_pipelines = {}
_current_model = None
_lock = threading.Lock()
# Models that ran out of VRAM with model offload; use sequential from then on.
_sequential_fallback = set()
# Unload the cached pipeline after this many idle seconds (like Ollama's
# keep_alive). Pipelines hold tens of GB of system RAM.
_keep_alive = 600
_last_used = time.time()


class ClientGone(Exception):
    """The app disconnected; stop generating."""


def load_models_config():
    """Load models.json, creating it with defaults on first run."""
    if MODELS_JSON.exists():
        with open(MODELS_JSON) as f:
            return json.load(f)

    MODELS_JSON.parent.mkdir(parents=True, exist_ok=True)
    with open(MODELS_JSON, "w") as f:
        json.dump(DEFAULT_MODELS, f, indent=2)
    print(f"Created {MODELS_JSON} with default models", flush=True)
    return DEFAULT_MODELS


def model_config(model_name):
    base = model_name.split(":")[0]
    config = load_models_config()
    cfg = config.get(base, config.get(model_name))
    if cfg is None:
        if "/" in base and not base.startswith("x/"):
            return base, {"source": base, "type": "huggingface"}
        raise ValueError(f"Unknown model '{model_name}'. Add it to {MODELS_JSON}")
    return base, cfg


def unload_all():
    """Drop every cached pipeline and give the VRAM back."""
    global _current_model
    _pipelines.clear()
    _current_model = None
    import gc
    gc.collect()
    if torch.cuda.is_available():
        torch.cuda.empty_cache()
    try:
        # Return freed heap pages to the OS; glibc otherwise keeps them.
        import ctypes
        ctypes.CDLL("libc.so.6").malloc_trim(0)
    except Exception:
        pass


def idle_unloader():
    """Background thread: drop the cached pipeline once it's been idle."""
    while True:
        time.sleep(30)
        if _keep_alive <= 0:
            continue
        with _lock:
            if _pipelines and time.time() - _last_used > _keep_alive:
                print(f"Idle for {_keep_alive}s; unloading {_current_model}", flush=True)
                unload_all()


def offload_mode(base, model_cfg):
    if base in _sequential_fallback:
        return "sequential"
    return model_cfg.get("offload", "model")


def load_pipeline(model_name):
    """Load a pipeline for the given model, with caching."""
    global _current_model

    from diffusers import AutoPipelineForText2Image

    base, model_cfg = model_config(model_name)

    if base in _pipelines:
        _current_model = base
        return _pipelines[base]

    # Free the previous model to save VRAM
    if _current_model and _current_model in _pipelines:
        print(f"Unloading {_current_model}...", flush=True)
        del _pipelines[_current_model]
        torch.cuda.empty_cache()

    source = model_cfg["source"]
    model_type = model_cfg.get("type", "huggingface")

    if model_type == "checkpoint":
        from diffusers import QwenImagePipeline
        from safetensors.torch import load_file

        base_model = model_cfg.get("base", "Qwen/Qwen-Image")
        print(f"Loading {base_model} pipeline + local checkpoint {source}...", flush=True)

        # Load to CPU only; CPU offload moves weights to the GPU as needed
        pipe = QwenImagePipeline.from_pretrained(
            base_model, torch_dtype=torch.bfloat16, device_map="cpu",
        )

        ckpt = load_file(source, device="cpu")
        transformer_state, text_encoder_state, vae_state = {}, {}, {}
        for k, v in ckpt.items():
            if k.startswith("model.diffusion_model."):
                transformer_state[k.replace("model.diffusion_model.", "")] = v
            elif k.startswith("text_encoders.") and ".transformer." in k:
                text_encoder_state[k.split(".transformer.", 1)[1]] = v
            elif k.startswith("text_encoders."):
                parts = k.split(".", 2)
                if len(parts) > 2:
                    text_encoder_state[parts[2]] = v
            elif k.startswith("vae."):
                vae_state[k] = v

        pipe.transformer.load_state_dict(transformer_state, strict=False)
        pipe.text_encoder.load_state_dict(text_encoder_state, strict=False)
        if vae_state:
            pipe.vae.load_state_dict(vae_state, strict=False)

        del ckpt, transformer_state, text_encoder_state, vae_state
        import gc
        gc.collect()
        torch.cuda.empty_cache()
        print("Checkpoint weights loaded to CPU.", flush=True)
    else:
        print(f"Loading {source} from HuggingFace...", flush=True)
        pipe = AutoPipelineForText2Image.from_pretrained(
            source, torch_dtype=torch.bfloat16, device_map="cpu",
        )

    offload = offload_mode(base, model_cfg)
    if offload == "sequential":
        # Streams weights to the GPU layer by layer: fits large models in
        # small VRAM at the cost of speed.
        pipe.enable_sequential_cpu_offload()
    else:
        # Moves whole components (text encoder, transformer, VAE) to the
        # GPU one at a time; the largest one must fit in VRAM.
        pipe.enable_model_cpu_offload()
    try:
        pipe.enable_attention_slicing()
    except Exception:
        pass

    print(f"Model ready: {base} (offload={offload})", flush=True)
    _pipelines[base] = pipe
    _current_model = base
    return pipe


class ImageGenHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.0"  # stream until close; no chunk framing needed

    def log_message(self, fmt, *args):
        pass

    def _json(self, obj):
        body = json.dumps(obj).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path == "/health":
            self._json({"status": "ok"})
        elif self.path == "/models":
            self._json(load_models_config())
        else:
            self.send_error(404)

    def do_POST(self):
        if self.path == "/api/generate":
            self._handle_generate()
        else:
            self.send_error(404)

    def _handle_generate(self):
        try:
            length = int(self.headers.get("Content-Length", 0))
            req = json.loads(self.rfile.read(length))
            model = req["model"]
            prompt = req.get("prompt", "")
            _, cfg = model_config(model)
        except Exception as e:
            body = json.dumps({"error": f"bad request: {e}"}).encode()
            self.send_response(400)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return

        width = min(max(int(req.get("width") or 1024), 256), 2048)
        height = min(max(int(req.get("height") or 1024), 256), 2048)
        steps = min(max(int(req.get("steps") or cfg.get("steps", 4)), 1), 50)
        guidance = float(cfg.get("guidance", DEFAULT_GUIDANCE))

        self.send_response(200)
        self.send_header("Content-Type", "application/x-ndjson")
        self.end_headers()

        def send_json(obj):
            try:
                self.wfile.write((json.dumps(obj) + "\n").encode())
                self.wfile.flush()
            except (BrokenPipeError, ConnectionResetError) as e:
                raise ClientGone() from e

        def progress_cb(pipe_inst, step, timestep, kwargs):
            # Raising here aborts the pipeline if the app went away.
            send_json({"model": model, "done": False,
                       "completed": int(step) + 1, "total": steps})
            return kwargs

        def run():
            pipe = load_pipeline(model)
            return pipe(
                prompt=prompt,
                width=width,
                height=height,
                num_inference_steps=steps,
                guidance_scale=guidance,
                callback_on_step_end=progress_cb,
            )

        oom = False
        try:
            with _lock:
                base, model_cfg = model_config(model)
                retry = False
                t0 = time.time()
                try:
                    result = run()
                except torch.OutOfMemoryError:
                    if offload_mode(base, model_cfg) == "sequential":
                        raise
                    retry = True
                if retry:
                    # Retry outside the except block: the exception's
                    # traceback keeps the first pipeline alive, so its VRAM
                    # can't be freed while we're still handling it.
                    print(f"{base}: out of VRAM with model offload; "
                          "retrying with sequential offload", flush=True)
                    unload_all()
                    _sequential_fallback.add(base)
                    t0 = time.time()
                    result = run()
                elapsed = time.time() - t0
                global _last_used
                _last_used = time.time()
                print(f"Generated {width}x{height} in {steps} steps, {elapsed:.1f}s ({model})", flush=True)

            buf = io.BytesIO()
            result.images[0].save(buf, format="PNG")
            send_json({
                "model": model,
                "done": True,
                "done_reason": "stop",
                "image": base64.b64encode(buf.getvalue()).decode(),
                "completed": steps,
                "total": steps,
                "total_duration": int(elapsed * 1e9),
            })
        except ClientGone:
            print("Client disconnected; generation cancelled", flush=True)
        except Exception as e:
            import traceback
            traceback.print_exc()
            oom = isinstance(e, torch.OutOfMemoryError)
            try:
                send_json({"error": str(e)})
            except ClientGone:
                pass
        if oom:
            # Outside the except block so the failed pipeline can be freed.
            with _lock:
                unload_all()


def main():
    global MODELS_JSON, _keep_alive

    parser = argparse.ArgumentParser(description="Ollama Linux app image generation runner")
    parser.add_argument("--port", type=int, default=11435)
    parser.add_argument("--models", type=Path, help=f"models.json path (default {MODELS_JSON})")
    parser.add_argument("--keep-alive", type=int, default=_keep_alive,
                        help="seconds to keep an idle model loaded; 0 = forever (default 600)")
    args = parser.parse_args()
    if args.models:
        MODELS_JSON = args.models
    _keep_alive = args.keep_alive
    threading.Thread(target=idle_unloader, daemon=True).start()

    load_models_config()

    print(f"Models config: {MODELS_JSON}", flush=True)
    server = ThreadingHTTPServer(("127.0.0.1", args.port), ImageGenHandler)
    server.daemon_threads = True
    print(f"Listening on http://127.0.0.1:{args.port}", flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
