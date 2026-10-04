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
print("4. Rendering docs/assets/webstore/webstore-screenshot-4.png (1280x800 remote browsing over SSH)...")
html_template = (ROOT / "packages/extension/popup.html").read_text()
css_content = (ROOT / "packages/extension/popup.css").read_text()
base_popup_html = html_template.replace("<link rel=\"stylesheet\" href=\"popup.css\">", f"<style>{css_content}\nbody {{ width: 380px; min-height: 520px; }}</style>")

remote_popup_html = base_popup_html.replace(
    "<span id=\"status-text\" class=\"connection-state\">Checking...</span>",
    "<span id=\"status-text\" class=\"connection-state connected\">Connected</span>"
).replace(
    "<span id=\"remote-status\" class=\"connection-state\">Checking...</span>",
    "<span id=\"remote-status\" class=\"connection-state connected\">Active (devbox)</span>"
).replace(
    "style=\"display:none;\"",
    "style=\"display:block;\""
).replace(
    "placeholder=\"user@remote-host\"",
    "value=\"ubuntu@devbox\""
).replace(
    "<span id=\"network-value\" class=\"network-value\" aria-hidden=\"true\">OFF</span>",
    "<span id=\"network-value\" class=\"network-value active\" aria-hidden=\"true\">ON</span>"
).replace(
    "<input id=\"network-toggle\" class=\"network-toggle\" type=\"checkbox\" role=\"switch\" aria-labelledby=\"network-label\" aria-describedby=\"network-ready network-description network-scope\" disabled>",
    "<input id=\"network-toggle\" class=\"network-toggle\" type=\"checkbox\" role=\"switch\" aria-labelledby=\"network-label\" aria-describedby=\"network-ready network-description network-scope\" checked>"
).replace(
    "<span id=\"network-ready\" class=\"network-readiness\" role=\"status\">Not ready</span>",
    "<span id=\"network-ready\" class=\"network-readiness\" role=\"status\" style=\"color:#34D399;\">Active</span>"
).replace(
    "<div class=\"loading-state\">Loading tabs...</div>",
    """<button type="button" class="tab-item active" style="margin-bottom:6px;">
        <span class="tab-info">
          <span class="tab-title">App Dashboard · Next.js</span>
          <span class="tab-url">http://localhost:3000/dashboard</span>
        </span>
        <span class="tab-badges"><span class="tab-badge">ACTIVE</span></span>
      </button>
      <button type="button" class="tab-item">
        <span class="tab-info">
          <span class="tab-title">FastAPI Documentation</span>
          <span class="tab-url">http://localhost:8000/docs</span>
        </span>
      </button>"""
).replace(
    "<span id=\"ext-version\" class=\"version\"></span>",
    "<span id=\"ext-version\" class=\"version\">v0.1.41</span>"
)

with open("/tmp/_mock_remote.html", "w") as f: f.write(remote_popup_html)
subprocess.run([chrome_bin, "--headless=new", "--disable-gpu", "--screenshot=/tmp/_mock_remote.png",
                "--window-size=380,680", "file:///tmp/_mock_remote.html"], capture_output=True)
os.remove("/tmp/_mock_remote.html")
with open("/tmp/_mock_remote.png", "rb") as f:
    b64_popup_remote = base64.b64encode(f.read()).decode("utf-8")
os.remove("/tmp/_mock_remote.png")

html_ws4 = f"""<!DOCTYPE html>
<html>
<head>
  <style>
    * {{ margin:0; padding:0; box-sizing:border-box; }}
    body {{
      width:1280px;
      height:800px;
      overflow:hidden;
      background: radial-gradient(circle at 75% 50%, #1E1B4B 0%, #0F172A 50%, #030712 100%);
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
      background: rgba(99, 102, 241, 0.15);
      border: 1px solid rgba(129, 140, 248, 0.3);
      color: #818CF8;
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
      background: #818CF8;
      box-shadow: 0 0 10px #818CF8;
    }}
    .popup-frame {{
      border-radius: 16px;
      box-shadow: 0 25px 50px -12px rgba(0, 0, 0, 0.8), 0 0 40px rgba(99, 102, 241, 0.25);
      border: 1px solid rgba(129, 140, 248, 0.3);
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
    <div class="badge">Secure Web Forwarding</div>
    <h1>Remote Browsing & Localhost Routing</h1>
    <p>Browse remote web applications, microservices, and admin panels through an authenticated SSH tunnel with zero plaintext fallback.</p>
    <div class="features">
      <div class="feat-item"><div class="feat-dot"></div> Direct <code>localhost</code> access to remote server ports</div>
      <div class="feat-item"><div class="feat-dot"></div> Encrypted SSH tunnel with per-user transport isolation</div>
      <div class="feat-item"><div class="feat-dot"></div> Fail-closed security with one-click profile switch</div>
    </div>
  </div>
  <div class="popup-frame">
    <img src="data:image/png;base64,{b64_popup_remote}">
  </div>
</body>
</html>"""
with open("/tmp/_ws4.html", "w") as f: f.write(html_ws4)
subprocess.run([chrome_bin, "--headless=new", "--disable-gpu", "--default-background-color=00000000",
                f"--screenshot={WEBSTORE / 'webstore-screenshot-4.png'}", "--window-size=1280,800", "file:///tmp/_ws4.html"], capture_output=True)
os.remove("/tmp/_ws4.html")

print("5. Rendering docs/assets/webstore/webstore-screenshot-5.png (1280x800 screenshots studio)...")
shots_popup_html = base_popup_html.replace(
    "<span id=\"status-text\" class=\"connection-state\">Checking...</span>",
    "<span id=\"status-text\" class=\"connection-state connected\">Connected</span>"
).replace(
    "<span id=\"remote-status\" class=\"connection-state\">Checking...</span>",
    "<span id=\"remote-status\" class=\"connection-state\">OFF</span>"
).replace(
    "<span id=\"ext-version\" class=\"version\"></span>",
    "<span id=\"ext-version\" class=\"version\">v0.1.41</span>"
).replace(
    "id=\"tab-btn-notes\" class=\"main-tab active\"",
    "id=\"tab-btn-notes\" class=\"main-tab\""
).replace(
    "id=\"tab-btn-shots\" class=\"main-tab\"",
    "id=\"tab-btn-shots\" class=\"main-tab active\""
).replace(
    "id=\"shots-badge\" class=\"tab-badge-count\">0</span>",
    "id=\"shots-badge\" class=\"tab-badge-count\">2</span>"
).replace(
    "id=\"panel-notes\" class=\"tab-panel\"",
    "id=\"panel-notes\" class=\"tab-panel\" hidden"
).replace(
    "id=\"panel-shots\" class=\"tab-panel\" role=\"tabpanel\" aria-labelledby=\"tab-btn-shots\" hidden>",
    "id=\"panel-shots\" class=\"tab-panel\" role=\"tabpanel\" aria-labelledby=\"tab-btn-shots\">"
).replace(
    "<span id=\"shots-dest-text\">Saved locally. Server paths appear after a successful mirror.</span>",
    "<span id=\"shots-dest-text\" style=\"color:#34D399;\">● Mirrored to ubuntu@devbox:/tmp/tether-screenshots/</span>"
).replace(
    "<div class=\"empty-state\">No screenshots captured yet. Click <b>Crop Area</b> or <b>Viewport</b> above.</div>",
    """<div class="shot-card">
        <div class="shot-top-row">
          <div class="shot-thumb-wrapper" style="background:#1E293B;display:flex;align-items:center;justify-content:center;color:#38BDF8;font-size:11px;font-weight:600;">CROP</div>
          <div class="shot-details">
            <div class="shot-header">
              <span class="shot-title">Header Nav Component</span>
            </div>
            <div class="shot-meta">1240×82 · /dashboard</div>
            <div class="shot-path-chip">devbox:/tmp/tether-screenshots/nav.png</div>
          </div>
        </div>
        <textarea class="shot-comment" rows="2" readonly>Make user menu keyboard accessible with Esc closing</textarea>
      </div>
      <div class="shot-card">
        <div class="shot-top-row">
          <div class="shot-thumb-wrapper" style="background:#1E293B;display:flex;align-items:center;justify-content:center;color:#34D399;font-size:11px;font-weight:600;">VIEW</div>
          <div class="shot-details">
            <div class="shot-header">
              <span class="shot-title">Pricing Grid Viewport</span>
            </div>
            <div class="shot-meta">1280×800 · /pricing</div>
            <div class="shot-path-chip">devbox:/tmp/tether-screenshots/pricing.png</div>
          </div>
        </div>
        <textarea class="shot-comment" rows="2" readonly>Ensure currency toggle matches selected billing cycle</textarea>
      </div>"""
).replace(
    "<div class=\"loading-state\">Loading tabs...</div>",
    """<button type="button" class="tab-item active">
        <span class="tab-info">
          <span class="tab-title">Billing & Subscription</span>
          <span class="tab-url">http://localhost:3000/pricing</span>
        </span>
        <span class="tab-badges"><span class="tab-badge">ACTIVE</span></span>
      </button>"""
)

with open("/tmp/_mock_shots.html", "w") as f: f.write(shots_popup_html)
subprocess.run([chrome_bin, "--headless=new", "--disable-gpu", "--screenshot=/tmp/_mock_shots.png",
                "--window-size=380,680", "file:///tmp/_mock_shots.html"], capture_output=True)
os.remove("/tmp/_mock_shots.html")
with open("/tmp/_mock_shots.png", "rb") as f:
    b64_popup_shots = base64.b64encode(f.read()).decode("utf-8")
os.remove("/tmp/_mock_shots.png")

html_ws5 = f"""<!DOCTYPE html>
<html>
<head>
  <style>
    * {{ margin:0; padding:0; box-sizing:border-box; }}
    body {{
      width:1280px;
      height:800px;
      overflow:hidden;
      background: radial-gradient(circle at 25% 50%, #064E3B 0%, #0F172A 50%, #030712 100%);
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
      box-shadow: 0 25px 50px -12px rgba(0, 0, 0, 0.8), 0 0 40px rgba(52, 211, 153, 0.25);
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
    <div class="badge">Visual Capture Studio</div>
    <h1>Area Crop, Viewport & Full Page Captures</h1>
    <p>Capture area crops, viewports, or full pages. Automatically save locally and mirror to your remote SSH server with ready-to-use file paths for AI coding agents.</p>
    <div class="features">
      <div class="feat-item"><div class="feat-dot"></div> Interactive drag-to-select Area Crop or Full Page</div>
      <div class="feat-item"><div class="feat-dot"></div> Instant mirroring to remote server directories</div>
      <div class="feat-item"><div class="feat-dot"></div> Formatted markdown reports ready for agent prompts</div>
    </div>
  </div>
  <div class="popup-frame">
    <img src="data:image/png;base64,{b64_popup_shots}">
  </div>
</body>
</html>"""
with open("/tmp/_ws5.html", "w") as f: f.write(html_ws5)
subprocess.run([chrome_bin, "--headless=new", "--disable-gpu", "--default-background-color=00000000",
                f"--screenshot={WEBSTORE / 'webstore-screenshot-5.png'}", "--window-size=1280,800", "file:///tmp/_ws5.html"], capture_output=True)
os.remove("/tmp/_ws5.html")

print("✓ All 5 Web Store screenshots processed successfully!")
