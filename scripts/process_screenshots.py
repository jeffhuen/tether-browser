#!/usr/bin/env python3
import base64
import os
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
ASSETS = ROOT / "docs" / "assets"
WEBSTORE = ASSETS / "webstore"
WEBSTORE.mkdir(parents=True, exist_ok=True)

img_action = ASSETS / "tether-in-action.png"
if not img_action.exists():
    img_action = ASSETS / "tether-in-action.webp"

img_popup_notes = ASSETS / "tether-popup.png"
img_popup_clean = ASSETS / "tether-popup-clean.png"

with open(img_action, "rb") as f:
    b64_action = base64.b64encode(f.read()).decode("utf-8")

with open(img_popup_notes, "rb") as f:
    b64_popup_notes = base64.b64encode(f.read()).decode("utf-8")

b64_popup_clean = b64_popup_notes
if img_popup_clean.exists():
    with open(img_popup_clean, "rb") as f:
        b64_popup_clean = base64.b64encode(f.read()).decode("utf-8")

chrome_bin = "/opt/google/chrome/chrome" if os.path.exists("/opt/google/chrome/chrome") else "google-chrome"

print("1. Rendering docs/assets/webstore/webstore-screenshot-1.png (1280x800 full browser)...")
html_ws1 = f"""<!DOCTYPE html>
<html>
<head>
  <style>
    * {{ margin:0; padding:0; box-sizing:border-box; }}
    body {{
      width:1280px;
      height:800px;
      overflow:hidden;
      background: #030712;
      display:flex;
      align-items:center;
      justify-content:center;
    }}
    img {{
      width:100%;
      height:100%;
      object-fit:cover;
      display:block;
    }}
  </style>
</head>
<body>
  <img src="data:image/png;base64,{b64_action}">
</body>
</html>"""
with open("/tmp/_ws1.html", "w") as f: f.write(html_ws1)
subprocess.run([chrome_bin, "--headless=new", "--disable-gpu", "--default-background-color=00000000",
                f"--screenshot={WEBSTORE / 'webstore-screenshot-1.png'}", "--window-size=1280,800", "file:///tmp/_ws1.html"], capture_output=True)
os.remove("/tmp/_ws1.html")

print("2. Rendering docs/assets/webstore/webstore-screenshot-2.png (1280x800 review notes & crop)...")
html_ws2 = f"""<!DOCTYPE html>
<html>
<head>
  <style>
    * {{ margin:0; padding:0; box-sizing:border-box; }}
    body {{
      width:1280px;
      height:800px;
      overflow:hidden;
      background: radial-gradient(circle at 75% 50%, #1E293B 0%, #0F172A 50%, #030712 100%);
      display:flex;
      align-items:center;
      justify-content:space-between;
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
      padding: 0 64px;
    }}
    .hero-text {{
      max-width: 520px;
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
      font-size: 38px;
      font-weight: 800;
      line-height: 1.2;
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
      gap: 14px;
    }}
    .feat-item {{
      display: flex;
      align-items: center;
      gap: 12px;
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
      box-shadow: 0 25px 50px -12px rgba(0, 0, 0, 0.8), 0 0 40px rgba(56, 189, 248, 0.2);
      border: 1px solid rgba(56, 189, 248, 0.3);
      overflow: hidden;
      height: 700px;
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
    <div class="badge">Visual Feedback & Notes</div>
    <h1>In-Page Component Reviews & Screenshots</h1>
    <p>Capture area crops, viewports, or full-page screenshots. Add notes directly to page components and copy structured reports for AI coding agents.</p>
    <div class="features">
      <div class="feat-item"><div class="feat-dot"></div> Pinned component review notes & feedback</div>
      <div class="feat-item"><div class="feat-dot"></div> One-click Area Crop, Viewport, or Full Page capture</div>
      <div class="feat-item"><div class="feat-dot"></div> Mirrored to local workstation & remote server</div>
    </div>
  </div>
  <div class="popup-frame">
    <img src="data:image/png;base64,{b64_popup_notes}">
  </div>
</body>
</html>"""
with open("/tmp/_ws2.html", "w") as f: f.write(html_ws2)
subprocess.run([chrome_bin, "--headless=new", "--disable-gpu", "--default-background-color=00000000",
                f"--screenshot={WEBSTORE / 'webstore-screenshot-2.png'}", "--window-size=1280,800", "file:///tmp/_ws2.html"], capture_output=True)
os.remove("/tmp/_ws2.html")

print("3. Rendering docs/assets/webstore/webstore-screenshot-3.png (1280x800 remote connection & tabs)...")
html_ws3 = f"""<!DOCTYPE html>
<html>
<head>
  <style>
    * {{ margin:0; padding:0; box-sizing:border-box; }}
    body {{
      width:1280px;
      height:800px;
      overflow:hidden;
      background: radial-gradient(circle at 25% 50%, #1E293B 0%, #0F172A 50%, #030712 100%);
      display:flex;
      align-items:center;
      justify-content:space-between;
      flex-direction: row-reverse;
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
      padding: 0 64px;
    }}
    .hero-text {{
      max-width: 520px;
      color: #F8FAFC;
    }}
    .badge {{
      display: inline-block;
      padding: 6px 14px;
      border-radius: 9999px;
      background: rgba(16, 185, 129, 0.15);
      border: 1px solid rgba(52, 211, 153, 0.3);
      color: #34D399;
      font-size: 13px;
      font-weight: 600;
      text-transform: uppercase;
      letter-spacing: 0.05em;
      margin-bottom: 20px;
    }}
    h1 {{
      font-size: 38px;
      font-weight: 800;
      line-height: 1.2;
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
      gap: 14px;
    }}
    .feat-item {{
      display: flex;
      align-items: center;
      gap: 12px;
      font-size: 15px;
      color: #E2E8F0;
    }}
    .feat-dot {{
      width: 8px;
      height: 8px;
      border-radius: 50%;
      background: #34D399;
      box-shadow: 0 0 10px #34D399;
    }}
    .popup-frame {{
      border-radius: 16px;
      box-shadow: 0 25px 50px -12px rgba(0, 0, 0, 0.8), 0 0 40px rgba(52, 211, 153, 0.2);
      border: 1px solid rgba(52, 211, 153, 0.3);
      overflow: hidden;
      height: 700px;
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
    <div class="badge">Isolated Automation</div>
    <h1>Remote Control & Dedicated Tab Groups</h1>
    <p>Seamless SSH connection links remote AI coding agents to your active workstation browser with full Passkey, Touch ID, and 2FA support.</p>
    <div class="features">
      <div class="feat-item"><div class="feat-dot"></div> One-click SSH reverse tunnel connection</div>
      <div class="feat-item"><div class="feat-dot"></div> Isolated Tether tab groups prevent workspace interference</div>
      <div class="feat-item"><div class="feat-dot"></div> Automatic discovery of live automation tabs</div>
    </div>
  </div>
  <div class="popup-frame">
    <img src="data:image/png;base64,{b64_popup_clean}">
  </div>
</body>
</html>"""
with open("/tmp/_ws3.html", "w") as f: f.write(html_ws3)
subprocess.run([chrome_bin, "--headless=new", "--disable-gpu", "--default-background-color=00000000",
                f"--screenshot={WEBSTORE / 'webstore-screenshot-3.png'}", "--window-size=1280,800", "file:///tmp/_ws3.html"], capture_output=True)
os.remove("/tmp/_ws3.html")

print("✓ All Web Store screenshots processed successfully!")
