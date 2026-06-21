const fs = require("node:fs");
const path = require("node:path");

const packageRoot = path.resolve(__dirname, "..");
const binaries = [
  "opencode-manager-linux-x64",
  "opencode-manager-linux-arm64",
  "opencode-manager-darwin-x64",
  "opencode-manager-darwin-arm64",
];

const missing = binaries.filter((binary) => !fs.existsSync(path.join(packageRoot, "dist", binary)));

if (missing.length > 0) {
  console.error("Cannot pack opencode-manager; missing prebuilt binaries:");
  for (const binary of missing) {
    console.error(`  dist/${binary}`);
  }
  process.exit(1);
}
