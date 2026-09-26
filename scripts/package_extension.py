#!/usr/bin/env python3
import os
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
EXT_DIR = ROOT / "packages" / "extension"
DIST_DIR = ROOT / "dist"
DIST_DIR.mkdir(exist_ok=True)
ZIP_PATH = DIST_DIR / "tether-extension-v0.1.37.zip"

print(f"Packaging Tether Chrome Extension from {EXT_DIR}...")

with zipfile.ZipFile(ZIP_PATH, "w", zipfile.ZIP_DEFLATED) as zf:
    for root, dirs, files in os.walk(EXT_DIR):
        # Skip temporary, hidden, or test files
        dirs[:] = [d for d in dirs if not d.startswith(".") and d != "__pycache__"]
        for file in files:
            if file.startswith(".") or file.endswith(".tmp"):
                continue
            file_path = Path(root) / file
            arcname = file_path.relative_to(EXT_DIR)
            if file == "manifest.json":
                import json
                with open(file_path, "r", encoding="utf-8") as f:
                    manifest_data = json.load(f)
                manifest_data.pop("key", None)
                cleaned_manifest = json.dumps(manifest_data, indent=2).encode("utf-8")
                zf.writestr(str(arcname), cleaned_manifest)
                print(f"  + {arcname} (stripped 'key' for Chrome Web Store compliance)")
            else:
                zf.write(file_path, arcname)
                print(f"  + {arcname}")

print(f"\n✓ Created Chrome Web Store bundle: {ZIP_PATH} ({ZIP_PATH.stat().st_size} bytes)")
