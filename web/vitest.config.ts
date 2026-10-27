import { defineConfig, mergeConfig } from "vitest/config";

import vite from "./vite.config";

export default mergeConfig(
  vite,
  defineConfig({
    test: {
      environment: "jsdom",
      setupFiles: ["src/test/setup.ts"],
      include: ["src/**/*.test.{ts,tsx}"],
      // The longest journeys (a route's first render, then typing) take 3 s alone, more with the files in parallel
      testTimeout: 15_000,
      coverage: { provider: "v8", include: ["src/features/**", "src/app/**"] },
    },
  }),
);
