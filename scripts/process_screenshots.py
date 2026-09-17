#!/usr/bin/env python3
import base64
import os
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
ASSETS = ROOT / "docs" / "assets"
WEBSTORE = ASSETS / "webstore"
WEBSTORE.mkdir(parents=True, exist_ok=True)

img1_path = ASSETS / "tether-in-action.webp"
img2_path = ASSETS / "tether-popup.webp"

with open(img1_path, "rb") as f:
    b64_action = base64.b64encode(f.read()).decode("utf-8")

with open(img2_path, "rb") as f:
    b64_popup = base64.b64encode(f.read()).decode("utf-8")

print("1. Rendering docs/assets/tether-in-action.png (1568x981)...")
html1 = f"""<!DOCTYPE html>
<html>
<head>
  <style>
    * {{ margin:0; padding:0; box-sizing:border-box; }}
    body {{ width:1568px; height:981px; overflow:hidden; background:transparent; display:flex; }}
    img {{ width:1568px; height:981px; display:block; }}
  </style>
</head>
<body>
  <img src="data:image/webp;base64,{b64_action}">
</body>
</html>"""
with open("/tmp/_shot1.html", "w") as f: f.write(html1)
subprocess.run(["google-chrome", "--headless=new", "--disable-gpu", "--default-background-color=00000000",
                f"--screenshot={ASSETS / 'tether-in-action.png'}", "--window-size=1568,981", "file:///tmp/_shot1.html"], capture_output=True)
os.remove("/tmp/_shot1.html")

print("2. Rendering docs/assets/tether-popup.png (768x1017)...")
html2 = f"""<!DOCTYPE html>
<html>
<head>
  <style>
    * {{ margin:0; padding:0; box-sizing:border-box; }}
    body {{ width:768px; height:1017px; overflow:hidden; background:transparent; display:flex; }}
    img {{ width:768px; height:1017px; display:block; }}
  </style>
</head>
<body>
  <img src="data:image/webp;base64,{b64_popup}">
</body>
</html>"""
with open("/tmp/_shot2.html", "w") as f: f.write(html2)
subprocess.run(["google-chrome", "--headless=new", "--disable-gpu", "--default-background-color=00000000",
                f"--screenshot={ASSETS / 'tether-popup.png'}", "--window-size=768,1017", "file:///tmp/_shot2.html"], capture_output=True)
os.remove("/tmp/_shot2.html")

print("3. Rendering docs/assets/webstore/webstore-screenshot-1.png (1280x800 full browser)...")
html_ws1 = f"""<!DOCTYPE html>
<html>
<head>
  <style>
    * {{ margin:0; padding:0; box-sizing:border-box; }}
    body {{
      width:1280px;
      height:800px;
      overflow:hidden;
      background:#0B0F19;
      display:flex;
      align-items:center;
      justify-content:center;
    }}
    img {{
      width:1280px;
      height:800px;
      object-fit:cover;
      display:block;
    }}
  </style>
</head>
<body>
  <img src="data:image/webp;base64,{b64_action}">
</body>
</html>"""
with open("/tmp/_ws1.html", "w") as f: f.write(html_ws1)
subprocess.run(["google-chrome", "--headless=new", "--disable-gpu", "--default-background-color=00000000",
                f"--screenshot={WEBSTORE / 'webstore-screenshot-1.png'}", "--window-size=1280,800", "file:///tmp/_ws1.html"], capture_output=True)
os.remove("/tmp/_ws1.html")

print("4. Rendering docs/assets/webstore/webstore-screenshot-2.png (1280x800 popup focus)...")
html_ws2 = f"""<!DOCTYPE html>
<html>
<head>
  <style>
    * {{ margin:0; padding:0; box-sizing:border-box; }}
    body {{
      width:1280px;
      height:800px;
      overflow:hidden;
      background: radial-gradient(circle at center, #1E293B 0%, #0F172A 50%, #030712 100%);
      display:flex;
      align-items:center;
      justify-content:center;
      gap: 64px;
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
      padding: 0 48px;
    }}
    .hero-text {{
      max-width: 480px;
      color: #F8FAFC;
    }}
    .badge {{
      display: inline-block;
      padding: 6px 14px;
      border-radius: 9999px;
      background: rgba(14, 165, 233, 0.15);
      border: 1px solid rgba(56, 189, 248, 0.3);
      color: #38BDF8;
      font-size: 13px;
      font-weight: 600;
      text-transform: uppercase;
      letter-spacing: 0.05em;
      margin-bottom: 20px;
    }}
    h1 {{
      font-size: 40px;
      font-weight: 800;
      line-height: 1.15;
      margin-bottom: 16px;
      background: linear-gradient(135deg, #FFFFFF 40%, #94A3B8 100%);
      -webkit-background-clip: text;
      -webkit-text-fill-color: transparent;
    }}
    p {{
      font-size: 16px;
      line-height: 1.6;
      color: #94A3B8;
      margin-bottom: 24px;
    }}
    .features {{
      display: flex;
      flex-direction: column;
      gap: 12px;
    }}
    .feat-item {{
      display: flex;
      align-items: center;
      gap: 10px;
      font-size: 15px;
      color: #E2E8F0;
    }}
    .feat-dot {{
      width: 8px;
      height: 8px;
      border-radius: 50%;
      background: #00F5FF;
      box-shadow: 0 0 10px #00F5FF;
    }}
    .popup-frame {{
      border-radius: 16px;
      box-shadow: 0 25px 50px -12px rgba(0, 0, 0, 0.7), 0 0 40px rgba(56, 189, 248, 0.15);
      border: 1px solid rgba(56, 189, 248, 0.25);
      overflow: hidden;
      height: 680px;
      display: flex;
      align-items: center;
    }}
    .popup-frame img {{
      height: 100%;
      width: auto;
      display: block;
    }}
  </style>
</head>
<body>
  <div class="hero-text">
    <div class="badge">In-Extension Control</div>
    <h1>Review Notes & Tab Management</h1>
    <p>Pin element-level feedback directly onto the page, copy structured Markdown reports for AI agents, and manage automated tabs.</p>
    <div class="features">
      <div class="feat-item"><div class="feat-dot"></div> One-click SSH reverse tunnel connection</div>
      <div class="feat-item"><div class="feat-dot"></div> Pinned component review notes</div>
      <div class="feat-item"><div class="feat-dot"></div> Tab group isolation & live discovery</div>
    </div>
  </div>
  <div class="popup-frame">
    <img src="data:image/webp;base64,{b64_popup}">
  </div>
</body>
</html>"""
with open("/tmp/_ws2.html", "w") as f: f.write(html_ws2)
subprocess.run(["google-chrome", "--headless=new", "--disable-gpu", "--default-background-color=00000000",
                f"--screenshot={WEBSTORE / 'webstore-screenshot-2.png'}", "--window-size=1280,800", "file:///tmp/_ws2.html"], capture_output=True)
os.remove("/tmp/_ws2.html")

print("✓ All screenshots processed successfully!")
