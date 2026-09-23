import { build } from "esbuild";

export async function buildTestBrowserBundle(entryPoint) {
  const result = await build({
    entryPoints: [entryPoint],
    bundle: true,
    format: "iife",
    platform: "browser",
    target: "es2020",
    write: false,
    minify: true,
    logLevel: "warning",
  });
  const output = result.outputFiles?.[0]?.text;
  if (!output)
    throw new Error(`test bundle was not produced for ${entryPoint}`);
  return output.replace(/<\/script/gi, "<\\/script");
}
