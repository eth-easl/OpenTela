# Installation

OpenFabric is provided as a pre-built binary for Linux machines with two architectures: `x86_64` and `arm64`. You can download the latest release from the [GitHub releases page](https://github.com/eth-easl/OpenFabric/releases), or with the command below:

* For `x86_64` architecture:*

```bash
wget https://github.com/eth-easl/OpenFabric/releases/latest/download/ocf-amd64 -O ocf && chmod +x ocf
```

* For `arm64` architecture:*

```bash
wget https://github.com/eth-easl/OpenFabric/releases/latest/download/ocf-arm64 -O ocf && chmod +x ocf
```

After downloading the binary, you can run it directly from the terminal. For example:

```bash
./ocf --help
OpenFabric is a decentralized fabric for running machine learning applications.

Usage:
  ocf [flags]
  ocf [command]

Available Commands:
  completion  Generate the autocompletion script for the specified shell
  help        Help about any command
  init        Initialize the system, create the database and the config file
  start       Start listening for incoming connections
  update      Update the Open Compute Binary
  version     Print the version of ocfcore

Flags:
      --config string   config file (default is $HOME/.config/ocf/cfg.yaml)
  -h, --help            help for ocf

Use "ocf [command] --help" for more information about a command.

```

## Build from source

You can also build OpenFabric from source. First, make sure you have `Go` installed on your machine. Then, clone the repository and build the binary:

```bash
git clone git@github.com:eth-easl/OpenFabric.git
cd src/
make build-release
```

This will create the the x86-amd64 version of `ocf` under $PROJECTROOT/src/build/release/. The expected output should look like this:

```bash
➜  make build-release
make[1]: Entering directory '/home/xiayao/Documents/projects/researchcomputer/OpenFabric/src'
go build -trimpath -trimpath -tags "" -ldflags "-w -X main.version=feat/update-docs -X main.commitHash=82e4d2d -X main.buildDate=2026-02-22T12:53:49+0100 -X main.authUrl= -X main.authClientId= -X main.authSecret= -X main.sentryDSN=" -o build/release ./entry...
make[1]: Leaving directory '/home/xiayao/Documents/projects/researchcomputer/OpenFabric/src'
mv build/release/entry build/release/ocf-amd64
make[1]: Entering directory '/home/xiayao/Documents/projects/researchcomputer/OpenFabric/src'
go build -trimpath -trimpath -tags "" -ldflags "-w -X main.version=feat/update-docs -X main.commitHash=82e4d2d -X main.buildDate=2026-02-22T12:53:49+0100 -X main.authUrl= -X main.authClientId= -X main.authSecret= -X main.sentryDSN=" -o build/release ./entry...
make[1]: Leaving directory '/home/xiayao/Documents/projects/researchcomputer/OpenFabric/src'
mv build/release/entry build/release/ocf-arm64
Build release complete. Built binaries:
-rwxr-xr-x 1 xiayao xiayao 52M Feb 22 12:53 build/release/ocf-amd64
-rwxr-xr-x 1 xiayao xiayao 49M Feb 22 12:53 build/release/ocf-arm64
```
