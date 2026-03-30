import { readFileSync } from "fs";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import electron from "vite-plugin-electron";
import renderer from "vite-plugin-electron-renderer";

const pkg = JSON.parse(readFileSync("./package.json", "utf-8"));

export default defineConfig({
  define: {
    __APP_VERSION__: JSON.stringify(pkg.version),
  },
  plugins: [
    react(),
    electron([
      {
        entry: "src/main/index.ts",
        vite: {
          build: {
            outDir: "dist-electron/main",
          },
        },
      },
      {
        entry: "src/main/preload.ts",
        vite: {
          build: {
            outDir: "dist-electron/main",
          },
        },
      },
    ]),
    renderer(),
  ],
});
