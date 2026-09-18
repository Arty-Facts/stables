# python:3.11-slim + pip CUDA wheels: torch's cu124 wheel bundles libcublasLt, libcudnn, etc.
# as nvidia-* pip packages and exposes them to the process, so onnxruntime-gpu can find them.
# The conda-based pytorch/pytorch image keeps those libs inside conda paths where
# onnxruntime-gpu's shared library can't locate them.
FROM python:3.11-slim

# Whether the machine running this has a usable GPU. `stables install tts` passes
# it, having checked for one. A CPU-only host otherwise gets several gigabytes of
# CUDA wheels and an onnxruntime that cannot use them.
ARG USE_GPU=1

# Whether to install the cloning engine. Opt-in: qwen-tts pins transformers to an
# exact version and pulls gradio, and its weights are gigabytes, which is a lot to
# carry for a machine that will never clone a voice.
ARG WITH_QWEN=0

WORKDIR /app

RUN apt-get update && apt-get install -y --no-install-recommends \
    libsndfile1 \
    curl \
    ffmpeg \
    && rm -rf /var/lib/apt/lists/*

COPY backend/requirements.txt /tmp/
COPY backend/requirements-qwen.txt /tmp/
# 1. Install torch + torchaudio from the official CUDA 12.4 index (bundles CUDA libs).
# 2. Strip torch/torchaudio from requirements so the bare 'torch' line can't pull the CPU wheel.
# 3. Install the rest of the requirements.
# 4. Install onnxruntime-gpu last so it replaces the CPU onnxruntime pulled by kokoro-onnx.
# Separate layers on purpose: a change to one must not re-download a wheel from
# another. The torch download is gigabytes.
RUN if [ "$USE_GPU" = "1" ]; then \
      # CUDA build: its nvidia-* wheel dependencies carry the libraries below.
      pip install --no-cache-dir torch torchaudio --index-url https://download.pytorch.org/whl/cu124; \
    else \
      # CPU build: a tenth of the size and nothing to fall back from.
      pip install --no-cache-dir torch torchaudio --index-url https://download.pytorch.org/whl/cpu; \
    fi

RUN sed '/^torch/d' /tmp/requirements.txt > /tmp/requirements-docker.txt \
 && pip install --no-cache-dir -r /tmp/requirements-docker.txt

RUN if [ "$WITH_QWEN" = "1" ]; then \
      # Before onnxruntime-gpu, deliberately: qwen-tts depends on plain
      # onnxruntime, so installing it after would quietly replace the GPU build
      # with the CPU one and cost the ~10x that pinning bought.
      pip install --no-cache-dir -r /tmp/requirements-qwen.txt; \
    fi

RUN if [ "$USE_GPU" = "1" ]; then \
      # Pinned, and the version matters more than it looks. The CUDA execution
      # provider is built against one CUDA and cuDNN generation: 1.21+ wants
      # CUDA 13 and cuDNN 9, which does not match the cu124 torch below, and it
      # does not fail loudly — it logs one line and runs on the CPU while still
      # appearing in get_available_providers(). 1.20.0 wants CUDA 12, which is
      # what the torch wheels here provide. Bump this and the torch index
      # together, and check the session's providers afterwards, not the list.
      # Installed last, so it replaces the CPU onnxruntime that kokoro-onnx
      # pulls in.
      pip install --no-cache-dir "onnxruntime-gpu==1.20.0"; \
    fi

# Register the CUDA libs installed by torch's nvidia-* pip deps with the system linker
# so onnxruntime-gpu's dlopen("libcublasLt.so.12") succeeds at InferenceSession init.
RUN find /usr/local/lib/python3.11/site-packages/nvidia -maxdepth 3 -type d -name "lib" \
    | sort > /etc/ld.so.conf.d/nvidia-pip.conf \
    && ldconfig

COPY backend/ ./backend/

# Weights are NOT in the image. `stables install tts` fetches them into
# ~/.stables/tts/models on the host and the compose file mounts them read-only at
# /app/models, so they are downloaded once and survive every container rebuild,
# image rebuild and `docker rm`.
#
# The old arrangement baked them in, precisely to avoid a bind mount that
# resolves to an empty directory: Docker creates a missing bind source as an empty
# directory, and the backend then answers /health with "healthy", reports zero
# voices and 404s every request. That failure is now caught instead of avoided —
# the installer reads /v1/voices after starting the stack and fails the install
# when the list is empty (see verifyVoices), naming this directory.
#
# Deliberately no VOLUME here. A VOLUME declaration would put an anonymous volume
# over this path and leave one behind on every recreate, hiding the mount.

EXPOSE 17493

HEALTHCHECK --interval=30s --timeout=10s --start-period=90s --retries=3 \
    CMD curl -f http://localhost:17493/health || exit 1

CMD ["python", "backend/start_server.py", "--host", "0.0.0.0", "--port", "17493"]
