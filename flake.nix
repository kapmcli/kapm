{
  description = "kamgr - Kiro Asset Manager CLI";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
        lib = pkgs.lib;

        indexionPlatform = {
          "aarch64-darwin" = "darwin-arm64";
          "x86_64-linux" = "linux-x64";
        }.${system} or (throw "indexion: unsupported system ${system}");

        indexionHash = {
          "darwin-arm64" = "1gbjfzwy9rgn7n79hj354w1jh2cqc6fvsj1m2zscvg6va6b1hdhl";
          "linux-x64" = "1pqll5vkb50fygq7ibqdry0lby54r50p17f75fv2s95xqy515c3i";
        }.${indexionPlatform};

        indexion = pkgs.stdenvNoCC.mkDerivation {
          pname = "indexion";
          version = "0.11.0";
          src = pkgs.fetchzip {
            url = "https://github.com/trkbt10/indexion/releases/download/v0.11.0/indexion-${indexionPlatform}.tar.gz";
            sha256 = indexionHash;
            stripRoot = true;
          };
          installPhase = ''
            mkdir -p $out/bin $out/share/indexion
            cp indexion $out/bin/
            cp -r kgfs $out/share/indexion/
          '';
        };
      in
      {
        devShell = pkgs.mkShell {
          packages = with pkgs; [
            go_1_26
            gopls
            just
            golangci-lint
            goreleaser
            git
            indexion
            uv
            vhs
            ttyd
            nodejs
            bash
          ];

          shellHook = ''
            #if [ -n "''${FENCE_SANDBOX:-}" ]; then
            #  # Remove Nix coreutils from PATH to avoid Fence sandbox
            #  # multicall binary collateral blocking (chroot deny blocks ls, cat, etc.)
            #  PATH=$(echo "$PATH" | tr ':' '\n' | grep -v '/nix/store/.*-coreutils' | tr '\n' ':' |
            #    sed 's/:$//')
            #  export PATH
            #fi

            export GOFLAGS="-tags=netgo"
            # Configure indexion KGF specs path
            _indexion_config_dir="$HOME/Library/Application Support/Indexion"
            if [ "$(uname)" = "Linux" ]; then
              _indexion_config_dir="''${XDG_CONFIG_HOME:-$HOME/.config}/Indexion"
            fi
            _indexion_config="$_indexion_config_dir/config.toml"
            if [ ! -f "$_indexion_config" ]; then
              mkdir -p "$_indexion_config_dir"
              echo '[global]' > "$_indexion_config"
              echo "kgfs_dir = \"${indexion}/share/indexion/kgfs\"" >> "$_indexion_config"
            fi
          '';
        };
      }
    );
}
