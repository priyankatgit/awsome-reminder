// Image compression for description attachments: decode, fit the longest
// side within 1600 px, encode WebP at q=0.8 and, if that is over 600 KB, again
// at q=0.6. Go accepts only WebP up to 2 MB, so this is what makes a pasted
// 4K screenshot fit. Decoding and resizing run in createImageBitmap, off the
// main thread in Chromium/WebView2; encoding uses OffscreenCanvas where there
// is one.

export const MAX_INPUT = 20 * 1024 * 1024;
export const MAX_SIDE = 1600;
export const TARGET_BYTES = 600 * 1024;

const ACCEPTED = ["image/png", "image/jpeg", "image/webp", "image/gif", "image/bmp"];
/** For the file picker's accept attribute. */
export const ACCEPT = ACCEPTED.join(",");

/** The size that fits width × height within MAX_SIDE on its longest side. */
export function compressTarget(width: number, height: number): { w: number; h: number } {
  const longest = Math.max(width, height);
  if (longest <= MAX_SIDE) return { w: width, h: height };
  const scale = MAX_SIDE / longest;
  return { w: Math.max(1, Math.round(width * scale)), h: Math.max(1, Math.round(height * scale)) };
}

/** The quality for the final encode, given the size of the q=0.8 one. */
export function pickQuality(sizeAtQ08: number): 0.8 | 0.6 {
  return sizeAtQ08 > TARGET_BYTES ? 0.6 : 0.8;
}

/** The inline message for an input too large to take, else null. */
export function checkInputSize(bytes: number): string | null {
  return bytes > MAX_INPUT ? "Image too large (max 20 MB)" : null;
}

/** The inline message for a file type we don't take, else null. */
export function checkImageType(mime: string): string | null {
  return ACCEPTED.includes(mime.toLowerCase()) ? null : "Use a PNG, JPEG, WebP, GIF or BMP image";
}

type Surface = { encode(quality: number): Promise<Blob> };

function surface(bitmap: ImageBitmap): Surface {
  const { width: w, height: h } = bitmap;
  if (typeof OffscreenCanvas !== "undefined") {
    const canvas = new OffscreenCanvas(w, h);
    const ctx = canvas.getContext("2d");
    if (!ctx) throw new Error("no 2D canvas");
    ctx.drawImage(bitmap, 0, 0);
    return { encode: (quality) => canvas.convertToBlob({ type: "image/webp", quality }) };
  }
  const canvas = document.createElement("canvas");
  canvas.width = w;
  canvas.height = h;
  const ctx = canvas.getContext("2d");
  if (!ctx) throw new Error("no 2D canvas");
  ctx.drawImage(bitmap, 0, 0);
  return {
    encode: (quality) =>
      new Promise((resolve, reject) =>
        canvas.toBlob((b) => (b ? resolve(b) : reject(new Error("encode failed"))), "image/webp", quality),
      ),
  };
}

/** Compresses an image (PNG, JPEG, WebP, GIF first frame, BMP) to WebP. */
export async function compressImage(file: Blob): Promise<Blob> {
  const decoded = await createImageBitmap(file);
  const { w, h } = compressTarget(decoded.width, decoded.height);
  let bitmap = decoded;
  if (w !== decoded.width || h !== decoded.height) {
    bitmap = await createImageBitmap(decoded, { resizeWidth: w, resizeHeight: h, resizeQuality: "high" });
    decoded.close();
  }
  try {
    const out = surface(bitmap);
    let blob = await out.encode(0.8);
    if (pickQuality(blob.size) === 0.6) blob = await out.encode(0.6);
    // Engines without a WebP encoder quietly hand back PNG.
    if (blob.type !== "image/webp") throw new Error("this system can't encode WebP");
    return blob;
  } finally {
    bitmap.close();
  }
}
