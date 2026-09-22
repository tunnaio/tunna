import { defineConfig } from "tsdown";

// ESM and CommonJS from one source, with declarations for each (ADR-0010).
// fixedExtension gives .mjs/.cjs and .d.mts/.d.cts, which package.json's
// exports map names explicitly so neither format depends on "type".
export default defineConfig({
  entry: {
    index: "src/index.ts",
    sign: "src/wire/sign.ts",
    encode: "src/wire/encode.ts",
    crc32c: "src/wire/crc32c.ts",
  },
  format: ["esm", "cjs"],
  platform: "neutral",
  target: "es2022",
  dts: true,
  fixedExtension: true,
  clean: true,
});
