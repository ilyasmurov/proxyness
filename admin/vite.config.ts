import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import path from "path";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  base: "/",
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  server: {
    port: 5173,
    proxy: {
      "/admin/api": {
        target: "https://proxyness.smurov.com",
        changeOrigin: true,
        secure: false,
      },
      "/api/admin": {
        target: "https://proxyness.smurov.com",
        changeOrigin: true,
        secure: false,
      },
    },
  },
});
