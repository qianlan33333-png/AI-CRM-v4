import { spawnSync } from "node:child_process";

const isExecutable = (candidate, spawn = spawnSync) => {
  try {
    const result = spawn(candidate, ["--version"], { stdio: "ignore" });
    return !result.error && result.status === 0;
  } catch {
    return false;
  }
};

export const chromiumBinaryCandidates = (env = process.env, platform = process.platform) => {
  const candidates = [env.AICRM_CHROMIUM_BINARY, env.CHROME_BIN].filter(Boolean);
  if (platform === "darwin") candidates.push("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome");
  candidates.push("google-chrome", "google-chrome-stable", "chromium", "chromium-browser");
  return [...new Set(candidates)];
};

export const resolveChromiumBinary = ({
  env = process.env,
  platform = process.platform,
  spawn = spawnSync,
} = {}) => {
  const candidate = chromiumBinaryCandidates(env, platform)
    .find(value => isExecutable(value, spawn));
  if (candidate) return candidate;
  throw new Error("Chromium binary is unavailable");
};
