{
  description = "OpenCode Manager workspace dashboard";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in {
      packages = forAllSystems (pkgs:
        let
          package = pkgs.buildGoModule {
            pname = "opencode-manager";
            version = "0.0.0";
            src = self;
            vendorHash = "sha256-RYJNDNTvx26TUX7mmn3xPKxS+QqKDYolh/lYQTad+hI=";
            subPackages = [ "cmd/opencode-manager" ];
            nativeBuildInputs = [ pkgs.makeWrapper ];

            # Module install/uninstall hooks execute in Linux containers, where
            # Nix store interpreter paths from the host are unavailable.
            dontPatchShebangs = true;

            postInstall = ''
              mkdir -p "$out/share/opencode-manager/scripts"
              cp -R modules "$out/share/opencode-manager/modules"
              cp scripts/postinstall.js "$out/share/opencode-manager/scripts/postinstall.js"
              mv "$out/bin/opencode-manager" "$out/bin/.opencode-manager-unwrapped"
              makeWrapper "$out/bin/.opencode-manager-unwrapped" "$out/bin/opencode-manager" \
                --run "${pkgs.nodejs}/bin/node \"$out/share/opencode-manager/scripts/postinstall.js\""
              ln -s opencode-manager "$out/bin/ocm"
            '';

            meta = {
              description = "Manage isolated coding-agent workspaces";
              homepage = "https://github.com/Mickael-Roger/opencode-manager";
              license = pkgs.lib.licenses.mit;
              mainProgram = "ocm";
              platforms = pkgs.lib.platforms.linux ++ pkgs.lib.platforms.darwin;
            };
          };
        in { default = package; opencode-manager = package; });
    };
}
