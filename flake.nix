{
  description = "codefly toolbox plugin: grpc";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = nixpkgs.legacyPackages.${system};

        # buildGoModule produces a reproducible binary from this repo's
        # go.mod. vendorHash workflow on first build:
        #   1. Run `nix build .#default`. Nix prints the expected sha256
        #      for the vendor tree (or fails with "got sha256-... want
        #      sha256-...").
        #   2. Replace `pkgs.lib.fakeHash` below with the printed value.
        #   3. `nix build` should then succeed and produce
        #      result/bin/toolbox-grpc.
        # The fakeHash is intentional — a real hash needs to be baked
        # in per release; CI's nix-build step prints it on first run.
        toolbox = pkgs.buildGoModule {
          pname = "toolbox-grpc";
          version = "0.0.1";
          src = pkgs.lib.cleanSource ./.;
          vendorHash = "sha256-8/cnuvd1rcjEtHkxIkWV9SjJzbdSiYvVupWZFtgWY/o=";
          # Build only the binary entrypoint, not the test packages —
          # `subPackages` keeps the nix build narrow + fast.
          subPackages = [ "cmd/toolbox-grpc" ];
          # Disable cgo — every codefly toolbox is pure Go today, and
          # cgo would drag in a C toolchain into the nix closure.
          env.CGO_ENABLED = "0";
          meta = with pkgs.lib; {
            description = "codefly toolbox plugin: grpc";
            homepage = "https://codefly.dev";
            platforms = platforms.unix;
          };
        };
      in
      {
        packages = {
          # Convenience aliases:
          default = toolbox;
          toolbox = toolbox;
          # The codefly host's AgentStore expects this exact attribute
          # name when fetching plugins from a flake — keep in sync with
          # core/agents/manager/store.go's NixStore.attrFor.
          "agents-toolbox-grpc-0.0.1" = toolbox;
        };

        # `nix develop` here lands in the same toolchain CI uses, so
        # contributors don't drift from the build environment.
        devShells.default = pkgs.mkShell {
          packages = [ pkgs.go_1_25 ];
        };
      }
    );
}
