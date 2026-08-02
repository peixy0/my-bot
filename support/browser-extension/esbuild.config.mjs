import * as esbuild from "esbuild";

const isWatch = process.argv.includes("--watch");

/** @type {esbuild.BuildOptions} */
const config = {
  entryPoints: ["src/service-worker.ts"],
  bundle: true,
  outfile: "dist/service-worker.js",
  format: "iife",
  target: ["chrome116"],
  platform: "browser",
  sourcemap: isWatch ? "inline" : false,
  logLevel: "info",
  // Chrome extension service workers don't have global window
  define: {
    "globalThis.chrome": "chrome",
  },
};

if (isWatch) {
  const ctx = await esbuild.context(config);
  await ctx.watch();
  console.log("[esbuild] watching src/…");
} else {
  await esbuild.build(config);
  console.log("[esbuild] built dist/service-worker.js");
}