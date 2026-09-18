# OpenTela

[![GitHub Repo](https://img.shields.io/badge/github-eth--easl%2FOpenTela-black?logo=github)](https://github.com/eth-easl/OpenTela) ![CI Workflow](https://github.com/eth-easl/OpenTela/actions/workflows/ci.yml/badge.svg) [![License](https://img.shields.io/github/license/eth-easl/OpenTela)](LICENSE) [![Ask DeepWiki](https://deepwiki.com/badge.svg)](https://deepwiki.com/eth-easl/OpenTela) [![Discord](https://img.shields.io/badge/Discord-%235865F2.svg?&logo=discord&logoColor=white)](https://discord.gg/pAsWxTYttP)

**OpenTela** (Aka: OpenFabric) is a distributed computing platform designed to orchestrate computing resources across a decentralized network. It leverages peer-to-peer networking, CRDT-based state management to create a resilient and scalable network of computing resources. It is used to power the [serving system at SwissAI Initiative](https://serving.swissai.cscs.ch).

Tela is the latin word for "Fabric", which refers to the interconnected network of computing resources that OpenTela manages.

## Latest Updates

* **[2026/04]** 🚀 **SwissAI Model Launch Toolkit**: For users with access to Alps HPC, you can now use the dedicated `sml` (SwissAI Model Launch) CLI tool to easily deploy your models and connect to the [SwissAI Research Platform](https://serving.swissai.svc.cscs.ch/) through OpenTela. Check out the [documentation](https://github.com/swiss-ai/model-launch) for details.
* **[2026/02]** 💡 **How SwissAI Leverages OpenTela**: We wrote a case study on how SwissAI uses OpenTela to orchestrate their distributed GPU nodes for scalable model serving. [Read more](docs/posts/swissai.md).

## Features

- **Decentralized Orchestration**: OpenTela eliminates the need for a central coordinator by using a gossip-based P2P network. It utilizes a Conflict-free Replicated Data Type (CRDT) registry to manage service discovery, health monitoring, and routing across distributed nodes. This architecture allows the system to remain operational and maintain a global view of resources even during network partitions.

- **Non-Invasive HPC Integration**: Designed specifically for the constraints of supercomputing environments, the system operates entirely as a user-space overlay. It bridges the gap between batch schedulers (like Slurm) and interactive serving engines (like vLLM or SGLang) without requiring root privileges or kernel modifications. This allows researchers to spin up "cloud-like" serving clusters using standard permissions.

- **Robust Fault Tolerance and Elasticity**: OpenTela is built for high-churn environments where resources are often volatile or preemptible (e.g., [scavenger queues](https://docs.icer.msu.edu/Scavenger_Queue/), [preemptible cloud instances](https://docs.cloud.google.com/compute/docs/instances/preemptible) or [slurm preemption](https://slurm.schedmd.com/preempt.html)). It utilizes peer-to-peer heartbeats to detect node failures within seconds, automatically marking failed nodes as "LEFT" and rerouting traffic to healthy replicas without service interruption.

## Adoption

- [teraflop-ai/model-serving](https://github.com/teraflop-ai/model-serving) provides minimal scripts of serving models on HPCs using OpenTela.
- OpenTela is used to power [SwissAI Serving](https://serving.swissai.cscs.ch/). It acts as the decentralized orchestration layer, routing inference requests to distributed GPU nodes while managing state, metrics, and peer discovery to ensure resilient and scalable model serving.

## Documentation

### Getting Started
- [Installation](docs/content/docs/tutorial/installation.mdx) — Download and install OpenTela
- [Spin Up LLM Serving](docs/content/docs/tutorial/spinup.mdx) — Set up multi-LLM serving cluster
- [Request Routing](docs/content/docs/tutorial/routing.mdx) — Understand how requests are routed
- [Wallet & Ownership](docs/content/docs/tutorial/owner.mdx) — Manage Solana wallets and node identity
- [Solana Settlement](docs/content/docs/tutorial/settlement.mdx) — Configure automated usage billing
- [Docker Serving](docs/content/docs/tutorial/docker-serving.mdx) — Use Docker containers for LLM serving
- [Glossary](docs/content/docs/tutorial/glossary.mdx) — Key terms and concepts

### Advanced Topics
- [CRDT Internals](docs/content/docs/advanced/crdt-internals.mdx) — How CRDT synchronization works
- [Security Hardening](docs/content/docs/advanced/security.mdx) — Build attestation, trust, and access control
- [Performance Benchmark](docs/content/docs/advanced/performance-optimization.mdx) — Proxy latency measurements
- [Large-Scale Simulation](docs/content/docs/advanced/benchmark.mdx) — Run 100+ node simulations

### Extensions
- [Fleet Manager](docs/content/docs/extensions/fleet-manager.mdx) — Deploy to SLURM clusters with otela-fleet

### Others
- [Artifact Guide for OSDI '26 Operational Systems](docs/content/docs/blog/osdi26-trace-artifact.mdx) — Access the SwissAI serving trace and reproduce the figures from our OSDI '26 paper.

## Contributing

Contributions are welcome! Please follow the code of conduct and submit pull requests for any enhancements or bug fixes.

## License

This project is licensed under the Apache v2 License - see the [LICENSE](LICENSE) file for details.

## Citation

If you found this repository helpful, please consider citing our work:

```
@inproceedings {318577,
  author = {Xiaozhe Yao and Youhe Jiang and Ilia Badanin and Qinghao Hu and Robert Matthew Smith and Binhang Yuan and Imanol Schlag and Eiko Yoneki and Ana Klimovic},
  title = {{OpenTela}: Unifying Decentralized Computing Resources for Heterogeneous {LLM} Serving (Operational Systems)},
  booktitle = {20th USENIX Symposium on Operating Systems Design and Implementation (OSDI 26)},
  year = {2026},
  isbn = {978-1-939133-55-7},
  address = {Seattle, WA},
  pages = {1821--1838},
  url = {https://www.usenix.org/conference/osdi26/presentation/yao},
  publisher = {USENIX Association},
  month = jul
}
```
