import argparse
import multiprocessing
import os
import sys
import subprocess
import shutil
from pathlib import Path

def run_command(cmd):
    print(f"Executing: {' '.join(cmd)}")
    result = subprocess.run(cmd, capture_output=False, text=True)
    if result.returncode != 0:
        print(f"Error: Command failed with exit code {result.returncode}")
        sys.exit(1)

def build(fast_mode=False):
    print("🚀 Starting Freeman Build Process...")
    if fast_mode:
        print("⚡ Fast Mode enabled: Disabling --onefile and heavy optimizations.")
    
    # Setup paths
    root = Path(__file__).parent
    dist = root / "dist"
    
    if dist.exists():
        shutil.rmtree(dist)
    dist.mkdir()

    # Nuitka Command
    cmd = [
        sys.executable, "-m", "nuitka",
        "--standalone",
        "--show-progress",
        f"--jobs={multiprocessing.cpu_count()}",
    ]

    if not fast_mode:
        cmd.append("--onefile")
        cmd.append("--plugin-enable=pylint-warnings")
        # Optimization for onefile startup
        cmd.append('--onefile-tempdir-spec="{CACHE_DIR}/freeman/onefile"')
    else:
        # Fast mode: disable heavy optimizations
        cmd.append("--lto=no")

    # Shared settings
    cmd.extend([
        # FastAPI/Uvicorn specific
        "--include-package=uvicorn",
        "--include-package=fastapi",
        "--include-package=starlette",
        # Kokoro/Torch specific
        "--include-package=kokoro",
        "--include-package=torch",
        "--include-package=numpy",
        "--include-package=soundfile",
        # Resources
        "--include-data-dir=static=static",
    ])

    # Include cached voices if they exist (to make the binary standalone)
    kokoro_cache = Path.home() / ".cache" / "kokoro"
    if kokoro_cache.exists():
        print(f"📦 Including cached voices from {kokoro_cache}...")
        # We'll map it to a local 'voices' dir in the binary
        cmd.append(f"--include-data-dir={kokoro_cache}=kokoro_cache")

    # Output and Entry point
    cmd.extend([
        "--output-dir=dist",
        "--output-filename=freeman",
        "src/cli.py"
    ])

    # Platform specific optimizations
    if sys.platform == "darwin":
        cmd.append("--macos-create-app-bundle")
        
    run_command(cmd)
    
    print(f"✅ Build complete! Binary available in {dist}")

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Build Freeman standalone binary")
    parser.add_argument("--fast", action="store_true", help="Fast build (standalone only, no onefile)")
    args = parser.parse_args()

    if shutil.which("nuitka") is None:
        print("❌ Nuitka not found. Please run: uv pip install nuitka")
        sys.exit(1)
    
    build(fast_mode=args.fast)
