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

def build():
    print("🚀 Starting Freeman Build Process...")
    
    # Setup paths
    root = Path(__file__).parent
    dist = root / "dist"
    src = root / "src"
    
    if dist.exists():
        shutil.rmtree(dist)
    dist.mkdir()

    # Nuitka Command
    cmd = [
        sys.executable, "-m", "nuitka",
        "--standalone",
        "--onefile",
        "--plugin-enable=pylint-warnings",
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
    ]

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
    
    print(f"✅ Build complete! Binary available in {dist}/freeman")

if __name__ == "__main__":
    if shutil.which("nuitka") is None:
        print("❌ Nuitka not found. Please run: pip install nuitka")
        sys.exit(1)
    build()
