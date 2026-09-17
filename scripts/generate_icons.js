const fs = require('fs');
const path = require('path');
const { execSync } = require('child_process');

const SIZES = [16, 32, 48, 128, 512];
const ICONS_DIR = path.join(__dirname, '../packages/extension/icons');
const SVG_PATH = path.join(ICONS_DIR, 'icon.svg');

if (!fs.existsSync(ICONS_DIR)) {
  fs.mkdirSync(ICONS_DIR, { recursive: true });
}

for (const size of SIZES) {
  const currentSvgPath = (size === 16) ? path.join(ICONS_DIR, 'icon16.svg') : SVG_PATH;
  const svgContent = fs.readFileSync(currentSvgPath, 'utf8');
  const html = `<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <style>
    * { margin: 0; padding: 0; box-sizing: border-box; }
    html, body {
      width: ${size}px;
      height: ${size}px;
      overflow: hidden;
      background: transparent;
    }
    svg {
      width: ${size}px;
      height: ${size}px;
      display: block;
    }
  </style>
</head>
<body>
  ${svgContent}
</body>
</html>`;

  const tmpHtml = path.join(ICONS_DIR, `_tmp_${size}.html`);
  const outPng = path.join(ICONS_DIR, `icon${size}.png`);
  fs.writeFileSync(tmpHtml, html);

  const cmd = `google-chrome --headless=new --disable-gpu --default-background-color=00000000 --force-device-scale-factor=1 --screenshot="${outPng}" --window-size=${size},${size} "file://${tmpHtml}"`;
  
  try {
    execSync(cmd, { stdio: 'pipe' });
    console.log(`✓ Rendered icon${size}.png (${size}x${size})`);
  } catch (err) {
    console.error(`Error rendering ${size}x${size}:`, err.message);
  } finally {
    fs.unlinkSync(tmpHtml);
  }
}

console.log("All extension icons rendered successfully!");
