#!/usr/bin/env sh
set -eu

if [ "$#" -eq 0 ]; then
  set -- nomic-embed-text qwen3:8b
fi

failed=""
for model in "$@"; do
  echo "stables: pulling $model"
  if ollama pull "$model"; then
    echo "stables: $model ready"
  else
    echo "stables: failed to pull $model" >&2
    failed="$failed $model"
  fi
done

if [ -n "$failed" ]; then
  echo "stables: failed models:$failed" >&2
  exit 1
fi
