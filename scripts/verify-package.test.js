const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");

const { packageErrors } = require("./verify-package");

const binaries = [
  "opencode-manager-linux-x64",
  "opencode-manager-linux-arm64",
  "opencode-manager-darwin-x64",
  "opencode-manager-darwin-arm64",
];

function packageFixture(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "opencode-manager-package-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  fs.mkdirSync(path.join(root, "bin"));
  fs.mkdirSync(path.join(root, "dist"));
  for (const binary of binaries) {
    fs.writeFileSync(path.join(root, "dist", binary), "");
  }
  for (const launcher of ["ocm", "opencode-manager"]) {
    fs.writeFileSync(path.join(root, "bin", launcher), "#!/usr/bin/env node\n", { mode: 0o755 });
  }
  return root;
}

test("packageErrors accepts executable launcher files", (t) => {
  assert.deepEqual(packageErrors(packageFixture(t)), []);
});

test("packageErrors rejects a non-executable launcher", (t) => {
  const root = packageFixture(t);
  fs.chmodSync(path.join(root, "bin", "opencode-manager"), 0o644);

  assert.match(packageErrors(root).join("\n"), /bin\/opencode-manager/);
});

test("packageErrors rejects a launcher directory", (t) => {
  const root = packageFixture(t);
  const launcher = path.join(root, "bin", "ocm");
  fs.unlinkSync(launcher);
  fs.mkdirSync(launcher);

  assert.match(packageErrors(root).join("\n"), /bin\/ocm/);
});
