import { readdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { brotliCompressSync, constants, gzipSync, zstdCompressSync } from "node:zlib";
import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// precompress writes .br, .gz and .zst copies next to each file, keeping only
// the ones smaller than the original. tailgate embeds them and serves the smallest one a client accepts
function precompress(): Plugin {
  const encoders: Record<string, (data: Uint8Array) => Uint8Array> = {
    br: (d) => brotliCompressSync(d, { params: { [constants.BROTLI_PARAM_QUALITY]: 11 } }),
    gz: (d) => gzipSync(d, { level: 9 }),
    zst: (d) => zstdCompressSync(d, { params: { [constants.ZSTD_c_compressionLevel]: 19 } }),
  };
  return {
    name: "precompress",
    apply: "build",
    closeBundle() {
      for (const name of readdirSync("dist", { recursive: true, encoding: "utf8" })) {
        if (!/\.(js|css|html|svg|json|txt)$/.test(name)) continue;
        const path = join("dist", name);
        const data = readFileSync(path);
        for (const [ext, encode] of Object.entries(encoders)) {
          const out = encode(data);
          if (out.length < data.length) writeFileSync(`${path}.${ext}`, out);
        }
      }
    },
  };
}

export default defineConfig({
  plugins: [react(), tailwindcss(), precompress()],
});
