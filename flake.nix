{
  description = "Go client library for the Foxglove WebSocket protocol";

  inputs =
    {
      nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

      flake-utils.url = "github:numtide/flake-utils";

      gitignore.url = "github:hercules-ci/gitignore.nix";
      gitignore.inputs.nixpkgs.follows = "nixpkgs";
    };

  outputs = inputs:
    let
      inherit (inputs) nixpkgs gitignore flake-utils;
      inherit (gitignore.lib) gitignoreSource;
    in
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
      in
      rec {
        formatter = pkgs.nixpkgs-fmt;
        packages.default = packages.foxglove-client-go;
        packages.foxglove-client-go = pkgs.buildGoModule {
          pname = "foxglove-client-go";
          version = pkgs.lib.fileContents ./version;
          src = gitignoreSource ./.;
          vendorHash = "sha256-1ckWAhiyEgGgIk/9Y/bgj4msc4eNVpsRgyIpxK8hDks=";
          # Only the example tools under cmd/ produce binaries; the library
          # itself (foxglove, cdr, ros2msg) is consumed as a Go module.
          subPackages = [ "cmd/foxglove-probe" "cmd/msggen" ];
          ldflags = [ "-s" "-w" ];
        };
        apps = rec {
          foxglove-probe = { type = "app"; program = "${packages.foxglove-client-go}/bin/foxglove-probe"; };
          msggen = { type = "app"; program = "${packages.foxglove-client-go}/bin/msggen"; };
          default = foxglove-probe;
        };
        devShell = pkgs.mkShell {
          packages = with pkgs; [ go git man busybox ];
        };
      }
    );
}
