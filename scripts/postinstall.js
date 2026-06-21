const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

const packageRoot = path.resolve(__dirname, "..");
const managerDir = path.join(userConfigDir(), "opencode-manager");
const modulesDir = path.join(managerDir, "modules");

fs.mkdirSync(managerDir, { mode: 0o700, recursive: true });
fs.mkdirSync(modulesDir, { mode: 0o700, recursive: true });

ensureConfig();
copyMissingEntries(path.join(packageRoot, "modules"), modulesDir);

function userConfigDir() {
  if (process.env.XDG_CONFIG_HOME) {
    return process.env.XDG_CONFIG_HOME;
  }
  if (process.platform === "darwin") {
    return path.join(os.homedir(), "Library", "Application Support");
  }
  return path.join(os.homedir(), ".config");
}

function ensureConfig() {
  const configPath = path.join(managerDir, "config.yaml");
  if (fs.existsSync(configPath)) {
    return;
  }

  const workspaceRoot = path.join(os.homedir(), ".local", "share", "opencode-manager");
  const config = [
    `workspaceRoot: ${yamlString(workspaceRoot)}`,
    "runtime: docker",
    "useLocalOpenCodeAuth: false",
    "baseImage:",
    "  name: debian:stable-slim",
    "  packages: []",
    "  commands: []",
    "moduleDirs:",
    `  - ${yamlString(modulesDir)}`,
    "",
  ].join("\n");

  fs.writeFileSync(configPath, config, { mode: 0o600 });
}

function copyMissingEntries(source, destination) {
  if (!fs.existsSync(source)) {
    return;
  }

  for (const entry of fs.readdirSync(source, { withFileTypes: true })) {
    if (entry.name === ".gitkeep") {
      continue;
    }

    const sourcePath = path.join(source, entry.name);
    const destinationPath = path.join(destination, entry.name);

    if (fs.existsSync(destinationPath)) {
      continue;
    }

    if (entry.isDirectory()) {
      fs.cpSync(sourcePath, destinationPath, { recursive: true, mode: fs.constants.COPYFILE_EXCL });
      continue;
    }

    if (entry.isFile()) {
      fs.copyFileSync(sourcePath, destinationPath, fs.constants.COPYFILE_EXCL);
    }
  }
}

function yamlString(value) {
  return JSON.stringify(value);
}
