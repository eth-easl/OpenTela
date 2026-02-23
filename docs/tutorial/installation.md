# Installation

OpenTela is provided as a pre-built binary for Linux machines with two architectures: `x86_64` and `arm64`. You can download the latest release from the [GitHub releases page](https://github.com/eth-easl/OpenTela/releases), or with the command below:

* For `x86_64` architecture:*

```bash
wget https://github.com/eth-easl/OpenTela/releases/latest/download/otela-amd64 -O otela && chmod +x otela
```

* For `arm64` architecture:*

```bash
wget https://github.com/eth-easl/OpenTela/releases/latest/download/otela-arm64 -O otela && chmod +x otela
```

After downloading the binary, you can run it directly from the terminal. For example:

```bash
./otela --help
OpenTela is a decentralized fabric for running machine learning applications.

Usage:
  otela [flags]
  otela [command]

Available Commands:
  completion  Generate the autocompletion script for the specified shell
  help        Help about any command
  init        Initialize the system, create the database and the config file
  start       Start listening for incoming connections
  update      Update the Open Compute Binary
  version     Print the version of otela

Flags:
      --config string   config file (default is $HOME/.config/ocf/cfg.yaml)
  -h, --help            help for otela

Use "otela [command] --help" for more information about a command.

```

## Build from source

You can also build OpenTela from source. First, make sure you have `Go` installed on your machine. Then, clone the repository and build the binary:

```bash
git clone git@github.com:eth-easl/OpenTela.git
cd src/
make build-release
```

This will create the the x86-amd64 version of `otela` under $PROJECTROOT/src/build/release/. The expected output should look like this:

```bash
➜  make build-release
make[1]: Entering directory '/home/xiayao/Documents/projects/researchcomputer/OpenTela/src'
go build -trimpath -trimpath -tags "" -ldflags "-w -X main.version=feat/update-docs -X main.commitHash=82e4d2d -X main.buildDate=2026-02-22T12:53:49+0100 -X main.authUrl= -X main.authClientId= -X main.authSecret= -X main.sentryDSN=" -o build/release ./entry...
make[1]: Leaving directory '/home/xiayao/Documents/projects/researchcomputer/OpenTela/src'
mv build/release/entry build/release/otela-amd64
make[1]: Entering directory '/home/xiayao/Documents/projects/researchcomputer/OpenTela/src'
go build -trimpath -trimpath -tags "" -ldflags "-w -X main.version=feat/update-docs -X main.commitHash=82e4d2d -X main.buildDate=2026-02-22T12:53:49+0100 -X main.authUrl= -X main.authClientId= -X main.authSecret= -X main.sentryDSN=" -o build/release ./entry...
make[1]: Leaving directory '/home/xiayao/Documents/projects/researchcomputer/OpenTela/src'
mv build/release/entry build/release/otela-arm64
Build release complete. Built binaries:
-rwxr-xr-x 1 xiayao xiayao 52M Feb 22 12:53 build/release/otela-amd64
-rwxr-xr-x 1 xiayao xiayao 49M Feb 22 12:53 build/release/otela-arm64
```
