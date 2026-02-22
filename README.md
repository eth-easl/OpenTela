# OpenFabric

**OpenFabric** (Aka: Open Compute Framework) is a robust, distributed computing platform designed to orchestrate computing resources across a decentralized network. It leverages peer-to-peer networking, CRDT-based state management to create a resilient and scalable research computer.

## Features

- **P2P Networking**: Built on top of `libp2p`, ensuring decentralized and resilient communication between nodes.
- **CRDT State Management**: Uses Conflict-free Replicated Data Types (CRDTs) for eventually consistent state across the network, including a robust **Tombstone Mechanism** to handle node departures cleanly.
- **High Performance**: Features optimized proxy handlers with request streaming and connection pooling to minimize latency and memory usage.
- **Observability**: Built-in metrics and logging, compatible with Prometheus and Axiom.

## Adoption

- OpenFabric is used to power [SwissAI Serving](https://serving.swissai.cscs.ch/). It acts as the decentralized orchestration layer, routing inference requests to distributed GPU nodes while managing state, metrics, and peer discovery to ensure resilient and scalable model serving.

## Architecture

OpenFabric consists of the following key components:

- **Node Table**: A CRDT-based registry of all active peers in the network.
- **Tombstone Manager**: Automatically cleans up metadata for nodes that have permanently left the network to prevent "ghost peers".
- **Proxy Server**: Handles external requests and routes them to the appropriate internal components or other nodes.
- **Entry Points**:
    - `src/entry/main.go`: The main Go entry point for the node server.
    - `main.py`: Python entry point (client/SDK).

## Getting Started

### Prerequisites

- **Go**: Version 1.25.0 or higher.
- **Docker**: (Optional) For containerized deployment.

### Installation

Clone the repository:

```bash
git clone https://github.com/your-org/OpenFabric.git
cd OpenFabric
```

Install Go dependencies:

```bash
cd src
go mod download
```

### Usage

To start the OpenFabric node:

```bash
cd src
go run entry/main.go start
```

For available commands:

```bash
cd src
go run entry/main.go --help
```

## Documentation

- [CRDT Tombstones](docs/crdt_tombstones.md)
- [Performance Optimization](docs/performance_optimization_proxy.md)

## Contributing

Contributions are welcome! Please follow the code of conduct and submit pull requests for any enhancements or bug fixes.

## License

This project is licensed under the GNU General Public License v3.0 - see the [LICENSE](LICENSE) file for details.
