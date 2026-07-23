const fs = require("node:fs");
const path = require("node:path");

const binaries = [
  "opencode-manager-linux-x64",
  "opencode-manager-linux-arm64",
  "opencode-manager-darwin-x64",
  "opencode-manager-darwin-arm64",
];

const launchers = ["bin/ocm", "bin/opencode-manager"];
function packageErrors(packageRoot) {
  const errors = [];
  const missing = binaries.filter((binary) => !fs.existsSync(path.join(packageRoot, "dist", binary)));
  if (missing.length > 0) {
    errors.push("Cannot pack opencode-manager; missing prebuilt binaries:", ...missing.map((binary) => `  dist/${binary}`));
  }

  const invalidLaunchers = launchers.filter((launcher) => {
    try {
      const stat = fs.statSync(path.join(packageRoot, launcher));
      return !stat.isFile() || (stat.mode & 0o111) === 0;
    } catch {
      return true;
    }
  });
  if (invalidLaunchers.length > 0) {
    errors.push("Cannot pack opencode-manager; launchers must be executable files:", ...invalidLaunchers.map((launcher) => `  ${launcher}`));
  }

  return errors;
}

if (require.main === module) {
  const errors = packageErrors(path.resolve(__dirname, ".."));
  if (errors.length > 0) {
    console.error(errors.join("\n"));
    process.exit(1);
  }
}

module.exports = { packageErrors };
