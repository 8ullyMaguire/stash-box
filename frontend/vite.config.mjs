/// <reference types="vitest" />
import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";
import graphqlPlugin from "vite-plugin-graphql-loader";
import analyzePlugin from "rollup-plugin-analyzer";

export default defineConfig(({ mode }) => {
  const env = {
    ...process.env,
    ...loadEnv(mode, process.cwd(), ""),
  };

  /** @type {import("vite").UserConfig} */
  const config = {
    base: env.VITE_BASE_PATH || "/",
    build: {
      outDir: "build",
      assetsDir: "assets",
      sourcemap: mode === "production",
    },
    optimizeDeps: {
      entries: "src/index.tsx",
    },
    server: {
      port: Number(env.PORT) || undefined,
    },
    plugins: [
      react(),
      graphqlPlugin(),
    ],
    resolve: {
      tsconfigPaths: true,
      alias: {
        src: new URL("./src", import.meta.url).pathname,
      },
    },
    test: {
      environment: "jsdom",
      globals: true,
      setupFiles: ["./src/test/setup.ts"],
      css: false,
      include: ["src/**/__tests__/**/*.test.{ts,tsx}"],
      // Raised from 15000. The suite grew from 39 files / 423 tests to 54 / 683
      // across #1215 and #1216, and the slowest form tests now sit close enough to
      // 15s that a loaded worker tips them over. That is NOT cosmetic: `user.type`
      // and `user.click` are real timers, so a test killed mid-interaction leaves the
      // submit callback half-called, and the next assertion sees it called TWICE --
      // "expected 1 time, got 2" -- which reads as a logic bug and is not one. Those
      // same tests pass 55/55 in isolation and the whole suite passes 683/683 here.
      // Diagnosed by raising the timeout rather than by reading the test: a failure
      // that vanishes when only the budget changes was never a logic failure.
      testTimeout: 60000,
      coverage: {
        provider: "v8",
        reporter: ["text", "html"],
        include: [
          "src/pages/**/*Form*.tsx",
          "src/pages/**/diff.ts",
          "src/pages/**/schema.ts",
          "src/utils/**/*.ts",
        ],
      },
    },
  };

  if (process.env.analyze) {
    config.plugins.push(
      analyzePlugin({ summaryOnly: true, limit: 30 })
    );
  }

  return config;
});
