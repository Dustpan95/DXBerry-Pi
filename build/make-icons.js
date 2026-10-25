// Draws station/web/icons/icon-192.png and icon-512.png: a white "DX" in blocks on the page's accent
// colour. Plain node (zlib only), no art from anywhere. Run: node build/make-icons.js
"use strict";
const fs = require("fs");
const path = require("path");
const zlib = require("zlib");

const ACCENT = [0x8e, 0x24, 0x53];
const WHITE = [0xff, 0xff, 0xff];
const D = ["1110", "1001", "1001", "1001", "1001", "1001", "1110"];
const X = ["10001", "10001", "01010", "00100", "01010", "10001", "10001"];
const COLS = D[0].length + 1 + X[0].length;
const ROWS = D.length;

function crc32(buf) {
  let c, crc = 0xffffffff;
  for (const b of buf) {
    c = (crc ^ b) & 0xff;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    crc = (crc >>> 8) ^ c;
  }
  return (crc ^ 0xffffffff) >>> 0;
}

function chunk(type, data) {
  const len = Buffer.alloc(4);
  len.writeUInt32BE(data.length);
  const body = Buffer.concat([Buffer.from(type, "ascii"), data]);
  const crc = Buffer.alloc(4);
  crc.writeUInt32BE(crc32(body));
  return Buffer.concat([len, body, crc]);
}

function icon(size) {
  const cell = Math.floor((size * 0.8) / COLS);
  const x0 = Math.floor((size - cell * COLS) / 2);
  const y0 = Math.floor((size - cell * ROWS) / 2);
  const rows = [];
  for (let y = 0; y < size; y++) {
    const row = Buffer.alloc(1 + size * 3); // filter byte 0, then RGB
    for (let x = 0; x < size; x++) {
      const gx = Math.floor((x - x0) / cell);
      const gy = Math.floor((y - y0) / cell);
      let on = false;
      if (x >= x0 && y >= y0 && gx < COLS && gy < ROWS) {
        on = gx < D[0].length ? D[gy][gx] === "1" : gx > D[0].length && X[gy][gx - D[0].length - 1] === "1";
      }
      const rgb = on ? WHITE : ACCENT;
      row[1 + x * 3] = rgb[0]; row[2 + x * 3] = rgb[1]; row[3 + x * 3] = rgb[2];
    }
    rows.push(row);
  }
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(size, 0); ihdr.writeUInt32BE(size, 4);
  ihdr[8] = 8; ihdr[9] = 2; // 8 bits, RGB
  return Buffer.concat([Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk("IHDR", ihdr), chunk("IDAT", zlib.deflateSync(Buffer.concat(rows), { level: 9 })), chunk("IEND", Buffer.alloc(0))]);
}

const out = path.join(__dirname, "..", "station", "web", "icons");
for (const size of [192, 512]) fs.writeFileSync(path.join(out, `icon-${size}.png`), icon(size));
