#!/usr/bin/env python3
"""
Inspect voices-v1.0.bin to verify structure before Rust implementation.
Run from repo root: python ratts/scripts/inspect_voices.py models/voices-v1.0.bin
"""
import sys
import numpy as np

path = sys.argv[1] if len(sys.argv) > 1 else "models/voices-v1.0.bin"

f = np.load(path)
print(f"Type: {type(f)}")
print(f"Keys ({len(f.files)}): {f.files[:8]}{'...' if len(f.files) > 8 else ''}")

first = f.files[0]
arr = f[first]
print(f"\nFirst voice '{first}':")
print(f"  shape: {arr.shape}")
print(f"  dtype: {arr.dtype}")
print(f"  min/max: {arr.min():.4f} / {arr.max():.4f}")

# Check all voices have same shape
shapes = {name: f[name].shape for name in f.files}
unique_shapes = set(shapes.values())
print(f"\nUnique shapes across all voices: {unique_shapes}")

print("\nAll voices:")
for name in sorted(f.files):
    print(f"  {name}: {f[name].shape} {f[name].dtype}")
