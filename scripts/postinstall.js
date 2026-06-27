const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

const packageRoot = path.resolve(__dirname, "..");
const managerDir = path.join(userConfigDir(), "opencode-manager");
const modulesDir = path.join(managerDir, "modules");

fs.mkdirSync(managerDir, { mode: 0o700, recursive: true });
fs.mkdirSync(modulesDir, { mode: 0o700, recursive: true });

ensureConfig();
removeLegacyFlatBuiltins(modulesDir);
syncModules(path.join(packageRoot, "modules"), modulesDir);

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
    "  name: docker.io/mroger78/ocm-base:latest",
    "  packages: []",
    "  commands: []",
    "moduleDirs:",
    `  - ${yamlString(modulesDir)}`,
    "",
  ].join("\n");

  fs.writeFileSync(configPath, config, { mode: 0o600 });
}

// syncModules copies every built-in module from the package's modules/ into the
// user config, preserving the category/module layout (e.g. cloud/aws) and
// overwriting each built-in module so updates take effect. User-authored modules
// (different category or name) are left untouched.
function syncModules(source, destination) {
  if (!fs.existsSync(source)) {
    return;
  }

  for (const category of fs.readdirSync(source, { withFileTypes: true })) {
    if (!category.isDirectory()) {
      continue;
    }

    const sourceCategory = path.join(source, category.name);
    const destinationCategory = path.join(destination, category.name);
    fs.mkdirSync(destinationCategory, { recursive: true });

    for (const mod of fs.readdirSync(sourceCategory, { withFileTypes: true })) {
      if (!mod.isDirectory()) {
        continue;
      }

      const sourcePath = path.join(sourceCategory, mod.name);
      const destinationPath = path.join(destinationCategory, mod.name);

      fs.rmSync(destinationPath, { recursive: true, force: true });
      fs.cpSync(sourcePath, destinationPath, { recursive: true });
    }
  }
}

// removeLegacyFlatBuiltins deletes the pre-category built-in module directories
// that older versions placed directly under modules/ (e.g. modules/aws). They
// would otherwise linger as orphans the categorized catalog no longer scans. A
// directory is only removed when it is a flat module (has a module.yml directly
// inside), so it never touches the new category directories.
function removeLegacyFlatBuiltins(destination) {
  const legacy = ["aws", "git", "kubernetes", "outscale", "ssh"];
  for (const name of legacy) {
    const dir = path.join(destination, name);
    if (fs.existsSync(path.join(dir, "module.yml"))) {
      fs.rmSync(dir, { recursive: true, force: true });
    }
  }
}

function yamlString(value) {
  return JSON.stringify(value);
}
