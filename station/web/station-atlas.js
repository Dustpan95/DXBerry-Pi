/* Orthographic view of the station's Maidenhead cell. The local land mask is
 * Natural Earth 1:110m land in a north-up equirectangular projection. */
const rad = Math.PI / 180;
const diameter = 432;
const radius = 184;
const center = diameter / 2;
let land;

async function landMask() {
  if (!land) {
    const image = new Image();
    image.src = "/art/land-mask.png";
    land = image.decode().then(() => {
      const buffer = document.createElement("canvas");
      buffer.width = image.naturalWidth;
      buffer.height = image.naturalHeight;
      const g = buffer.getContext("2d");
      g.drawImage(image, 0, 0);
      return g.getImageData(0, 0, buffer.width, buffer.height);
    }).catch(error => { land = null; throw error; });
  }
  return land;
}

// Light linework in geographic degrees. Lines converge with the projection.
function onLine(degrees, interval, thickness) {
  return Math.abs(degrees - interval * Math.round(degrees / interval)) < thickness;
}

export async function paintGlobe(canvas, point) {
  const mask = await landMask();
  const g = canvas.getContext("2d");
  canvas.width = diameter;
  canvas.height = diameter;
  const pixels = g.createImageData(diameter, diameter);
  const sinLat = Math.sin(point.lat * rad);
  const cosLat = Math.cos(point.lat * rad);
  for (let y = 0; y < diameter; y++) {
    const north = (center - y - 0.5) / radius;
    for (let x = 0; x < diameter; x++) {
      const east = (x + 0.5 - center) / radius;
      const square = 1 - east * east - north * north;
      if (square < 0) continue;
      const face = Math.sqrt(square);
      const lat = Math.asin(Math.max(-1, Math.min(1, north * cosLat + face * sinLat))) / rad;
      const lon = point.lon + Math.atan2(east, face * cosLat - north * sinLat) / rad;
      const col = Math.floor(((lon + 540) % 360) * mask.width / 360);
      const row = Math.min(mask.height - 1, Math.floor((90 - lat) * mask.height / 180));
      const dry = mask.data[(row * mask.width + col) * 4] > 127;
      const line = onLine(lat, 15, .22) || onLine(lon, 15, .22);
      const shade = 0.79 + 0.21 * face;
      const idx = (y * diameter + x) * 4;
      pixels.data[idx] = (dry ? 158 : 28) * shade + (line ? 24 : 0);
      pixels.data[idx + 1] = (dry ? 193 : 61) * shade + (line ? 24 : 0);
      pixels.data[idx + 2] = (dry ? 177 : 66) * shade + (line ? 24 : 0);
      pixels.data[idx + 3] = 255;
    }
  }
  g.putImageData(pixels, 0, 0);
  g.translate(center, center);
  g.strokeStyle = "#91b1a6";
  g.lineWidth = 1.2;
  g.beginPath();
  g.arc(0, 0, radius + 2, 0, Math.PI * 2);
  g.stroke();
  for (let deg = 0; deg < 360; deg += 5) {
    g.save();
    g.rotate(deg * rad);
    g.beginPath();
    g.moveTo(0, -radius - 6);
    g.lineTo(0, -radius - (deg % 30 ? 10 : 17));
    g.stroke();
    g.restore();
  }
  // Mark the cell center, rather than implying the exact antenna location is known.
  g.strokeStyle = "#172f31";
  g.lineWidth = 3;
  g.fillStyle = "#ffc077";
  g.beginPath();
  g.arc(0, 0, 6.5, 0, Math.PI * 2);
  g.fill();
  g.stroke();
  g.beginPath();
  g.arc(0, 0, 14, 0, Math.PI * 2);
  g.stroke();
  g.fillStyle = "#d7e5de";
  g.font = "bold 13px monospace";
  g.textAlign = "center";
  g.fillText("N", 0, -radius - 20);
}
