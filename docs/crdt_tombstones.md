---
title: "CRDT Refactoring: Tombstone Mechanism for Node Departure"
date: "2026-02-17"
tldr: "Introduced an explicit 'StatusLeft' and Tombstone Manager to reliably handle and cleanup nodes that permanently leave the network."
---

# CRDT Refactoring: Tombstone Mechanism for Node Departure

## Overview
The OpenFabric P2P network relies on a CRDT-based Node Table to track peers. Previously, when a node left the network, it would simply attempt to delete its key from the CRDT. However, due to the nature of CRDTs and eventual consistency, these deletions were not always propagated effectively, leading to "ghost peers" that remained in the table indefinitely.

## Problem
1.  **Ghost Peers**: Nodes that left were sometimes re-added by other peers who hadn't seen the delete operation yet, or the delete operation was lost/overridden by an older update.
2.  **Resource Leaks**: The system kept trying to connect to these ghost peers, wasting resources on dial attempts.

## Solution: Explicit Tombstones
We moved from an "immediate delete" model to an "announce leave" model with explicit tombstones.

### 1. StatusLeft
We introduced a new peer status constant `LEFT`.
-   **Old Behavior**: Node calls `store.Delete(key)`.
-   **New Behavior**: Node calls `AnnounceLeave()`, which updates its own record to `Status = LEFT`.

This ensures that the "departure" is a state update that propagates via the normal CRDT synchronization mechanism, guaranteeing that all peers eventually see that the node has left.

### 2. AnnounceLeave
The `DeleteNodeTable` function was replaced by `AnnounceLeave`.
When a node shuts down:
1.  It updates its `Peer` object: `Status = LEFT`, `Connected = false`, `LastSeen = Now()`.
2.  It publishes this update to the CRDT.

### 3. Immediate In-Memory Cleanup
When a peer receives an update (via `UpdateNodeTableHook`) where `Status == LEFT`:
-   It immediately removes the peer from its in-memory `NodeTable`.
-   This stops any new logic from trying to interact with that peer.

### 4. Tombstone Manager
To prevent the CRDT from growing indefinitely with "Left" nodes, we implemented a `TombstoneManager`.
-   **Role**: Periodically scans the CRDT for nodes with `Status == LEFT`.
-   **Logic**: If a node has been "Left" for longer than the `TombstoneRetention` period (default 24h), it is permanently deleted from the underlying datastore.
-   **Integration**: Runs as part of the existing `TombstoneCompactor` loop.

## Verification
New tests were added to `src/internal/protocol/node_table_test.go` to verify this behavior.
Run the specific test:
```bash
go test -v -run TestNodeLeave ocf/internal/protocol
```
