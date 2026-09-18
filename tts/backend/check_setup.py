#!/usr/bin/env python3
"""
Check if the TTS environment is properly set up.
"""

import sys
import importlib
import subprocess

def check_package(package_name, import_name=None):
    """Check if a package is installed and can be imported."""
    if import_name is None:
        import_name = package_name
    
    try:
        importlib.import_module(import_name)
        return True, "✓"
    except ImportError:
        return False, "✗"

def main():
    print("🔍 TTS Environment Setup Check")
    print("=" * 40)
    
    # Core packages
    core_packages = [
        ("pyperclip", None),
        ("prompt_toolkit", "prompt_toolkit"),
        ("soundfile", None),
        ("sounddevice", None),
        ("requests", None),
        ("numpy", None),
    ]
    
    print("\n📦 Core TUI Dependencies:")
    all_core_ok = True
    for pkg, import_name in core_packages:
        ok, status = check_package(pkg, import_name)
        print(f"  {status} {pkg}")
        if not ok:
            all_core_ok = False
    
    # Server packages (optional)
    server_packages = [
        ("torch", None),
        ("torchaudio", None),
        ("kokoro_onnx", None),
        ("fastapi", None),
        ("uvicorn", None),
        ("onnxruntime", None),
    ]
    
    print("\n🖥️  Server Dependencies (Optional):")
    server_ok = True
    for pkg, import_name in server_packages:
        ok, status = check_package(pkg, import_name)
        print(f"  {status} {pkg}")
        if not ok:
            server_ok = False
    
    # Check GPU availability
    print("\n🔧 Hardware Check:")
    try:
        import torch
        if torch.cuda.is_available():
            print(f"  ✓ CUDA available - {torch.cuda.device_count()} GPU(s)")
            print(f"    - {torch.cuda.get_device_name(0)}")
        else:
            print("  ⚠️  CUDA not available (CPU-only mode)")
    except ImportError:
        print("  ⚠️  PyTorch not installed - cannot check CUDA")
    
    # Environment check
    print("\n🐍 Python Environment:")
    print(f"  Python version: {sys.version}")
    try:
        venv = sys.prefix != sys.base_prefix
        print(f"  Virtual environment: {'Yes' if venv else 'No'}")
        if venv:
            print(f"  Environment path: {sys.prefix}")
    except:
        print("  Virtual environment: Unknown")
    
    print("\n" + "=" * 40)
    
    if all_core_ok:
        print("✅ Core dependencies: ALL OK")
        print("   You can run the TTS TUI client!")
        
        if server_ok:
            print("✅ Server dependencies: ALL OK") 
            print("   You can run the TTS server locally!")
        else:
            print("⚠️  Server dependencies: MISSING")
            print("   Install with: pip install kokoro-onnx fastapi uvicorn torch torchaudio")
    else:
        print("❌ Core dependencies: MISSING")
        print("   Install with: pip install -r requirements.txt")
        
    print("\n🚀 Next steps:")
    if all_core_ok and server_ok:
        print("   1. Start Kokoro server: python start_server.py --host 0.0.0.0 --port 17493")
        print("   2. Test connection: python test_connection.py")
        print("   3. Run TUI: python TTS_TUI.py --server http://localhost:17493")
    elif all_core_ok:
        print("   1. Connect to remote Kokoro server via SSH tunnel")
        print("   2. Test connection: python test_connection.py")
        print("   3. Run TUI: python TTS_TUI.py --server http://localhost:17493")
    else:
        print("   1. Install missing dependencies")
        print("   2. Re-run this check: python check_setup.py")

if __name__ == "__main__":
    main()
