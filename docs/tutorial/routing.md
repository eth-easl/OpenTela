# Routing

OpenTela routes incoming requests from users to the correct worker node in your cluster. This document explains how routing works, how identity groups control which requests reach which nodes, and how to use each endpoint.

## Overview

When a user sends a request to the head node, OpenTela needs to decide *which* worker should handle it. The routing layer answers that question using three URL path prefixes, each backed by a different handler:

| Prefix | Handler | Purpose |
| :--- | :--- | :--- |
| `/v1/service/:service/*path` | Global Service Forward | Route by **service name + identity group** — the typical path for end users |
| `/v1/p2p/:peerId/*path` | P2P Forward | Route to a **specific peer** by its Peer ID |
| `/v1/_service/:service/*path` | Local Service Forward | Forward to a **locally running service** on the current node (internal) |

Most users will only interact with `/v1/service/`. The other two exist for direct peer access and internal plumbing.

## Request flow

A typical request goes through two hops:

```
User ──► Head Node ──► Worker Node ──► Local Service (e.g. vLLM)
         /v1/service/llm/...           /v1/_service/llm/...
         (GlobalServiceForward)        (LocalServiceForward)
```

1. **User → Head Node**: The user sends a request to `/v1/service/llm/v1/chat/completions` on the head node.
2. **Candidate selection**: The head node reads the request body, queries the distributed node table for all connected peers that provide the `llm` service, and filters them by **identity group** (explained below).
3. **Load balancing**: One candidate is selected at random from the matching set.
4. **P2P forwarding**: The request is forwarded over libp2p to the selected worker at `/v1/_service/llm/v1/chat/completions`.
5. **Local forwarding**: The worker's Local Service Forward handler proxies the request to the local process (e.g., `localhost:8080` where vLLM is listening).
6. **Response**: The response streams back through the same chain to the user. An `X-Computing-Node` header is added so the caller knows which peer served the request.

## Identity groups

An **identity group** is a label attached to a service on a worker node. It tells the router *what kind of requests* that worker can handle. Identity groups use a `key=value` format. See the [glossary](glossary.md) for more background.

For example, when a worker starts serving `Qwen/Qwen3-8B` via vLLM, OpenTela automatically queries the model endpoint and registers the identity group `model=Qwen/Qwen3-8B` for that worker's `llm` service. You can see this in the node table at `/v1/dnt/table`:

```json
{
  "service": [
    {
      "name": "llm",
      "identity_group": ["model=Qwen/Qwen3-8B"]
    }
  ]
}
```

When a user request arrives at the head node, the router extracts the matching key (e.g. `model`) from the JSON request body and compares its value against each provider's identity group entries. Only providers whose identity group matches are considered as candidates.

### Matching modes

There are three ways an identity group entry can match a request:

#### Exact match: `key=value`

The standard mode. The router looks for a JSON field named `key` in the request body and checks whether its value equals `value` exactly.

A worker registered with `model=Qwen/Qwen3-8B` will only match requests whose JSON body contains `"model": "Qwen/Qwen3-8B"`.

#### Wildcard match: `key=*`

The router checks that the request body contains a field named `key`, but accepts **any value**. This is useful when a worker can handle multiple variants of a service and you don't want to enumerate every possibility.

A worker registered with `model=*` will match any request that has a `"model"` field in the body, regardless of which model is requested.

#### Catch-all: `all`

The special string `all` (without any `=`) matches **every request unconditionally**, regardless of what's in the body. This is useful for a fallback worker that should absorb any request that no other, more specific worker handles.

A worker registered with identity group `["all"]` will be a candidate for every incoming request to that service.

### Priority and fallback

Matching modes are organized into three priority tiers. By default, the router uses **strict matching** — only exact matches are considered. Users can opt in to lower-priority tiers by setting the `X-Otela-Fallback` request header:

| `X-Otela-Fallback` | Tiers considered | Behavior |
| :--- | :--- | :--- |
| *not set* or `0` | Exact only | Strict — request fails if no exact match exists |
| `1` | Exact, then Wildcard | Falls back to `key=*` providers when no exact match exists |
| `2` | Exact, then Wildcard, then Catch-all | Falls back through all tiers |

The three priority tiers are:

| Priority | Match type | Example |
| :--- | :--- | :--- |
| 1 (highest) | Exact (`key=value`) | `model=Qwen/Qwen3-8B` |
| 2 | Wildcard (`key=*`) | `model=*` |
| 3 (lowest) | Catch-all (`all`) | `all` |

If a provider has multiple identity group entries (e.g. `["model=Qwen/Qwen3-8B", "all"]`), the **best match** determines which tier that provider falls into. In this example the provider would land in the exact tier, not the catch-all tier.

**Example scenario**: Suppose you have three workers:

| Worker | Identity group |
| :--- | :--- |
| Worker A | `model=Qwen/Qwen3-8B` |
| Worker B | `model=*` |
| Worker C | `all` |

With `X-Otela-Fallback: 2`, a request with `"model": "Qwen/Qwen3-8B"` matches all three workers, but the router picks only from the exact tier — **Worker A** handles the request. If Worker A goes offline, the router falls back to the wildcard tier (**Worker B**). If Worker B also goes offline, the catch-all tier is used (**Worker C**).

With `X-Otela-Fallback: 1`, the same request would try Worker A first, then Worker B, but **would not** fall back to Worker C (catch-all is not enabled at level 1).

Without the header (or `X-Otela-Fallback: 0`), the request only considers Worker A. If Worker A is offline, the request fails with a 503 error — no fallback occurs.

A request with `"model": "Llama-3-70B"` and `X-Otela-Fallback: 2` does not match Worker A exactly, so the router picks from the wildcard tier — **Worker B**. Worker C is only used if Worker B is also unavailable.

### Matching rules

- A provider needs **at least one** identity group entry to match for it to be selected as a candidate.
- If a service has **no identity group entries** (empty list), it will **never** be selected. Make sure every worker has at least one identity group configured.
- Multiple identity group entries on a single service act as an **OR** — any single match is sufficient. The highest-priority match determines the tier.
- Within a tier, if multiple providers match, one is chosen at random (uniform distribution).

### Summary

| Identity group value | Matches when… | Priority |
| :--- | :--- | :--- |
| `model=Qwen/Qwen3-8B` | Request body has `"model": "Qwen/Qwen3-8B"` | 1 (highest) |
| `model=*` | Request body has a `"model"` field (any value) | 2 |
| `all` | Always | 3 (lowest) |

## Endpoints in detail

### Global Service Forward — `/v1/service/:service/*path`

This is the primary endpoint for users. It routes the request to a suitable worker based on service name and identity group matching.

**Supported methods**: `GET`, `POST`, `PATCH`

**URL parameters**:

- `:service` — the service name (e.g., `llm`)
- `*path` — the rest of the path, forwarded to the worker (e.g., `/v1/chat/completions`)

**Request headers**:

- `X-Otela-Fallback` *(optional)* — controls how aggressively the router falls back to lower-priority providers. `0` (default) = exact only, `1` = allow wildcard, `2` = allow wildcard + catch-all. See [Priority and fallback](#priority-and-fallback).

**Example** — send a chat completion request to any node serving `Qwen/Qwen3-8B`:

```python
import requests

response = requests.post(
    "http://<HEAD_NODE_IP>:8092/v1/service/llm/v1/chat/completions",
    headers={
        "Authorization": "Bearer test-token",
        "Content-Type": "application/json"
    },
    json={
        "model": "Qwen/Qwen3-8B",
        "messages": [{"role": "user", "content": "Hello!"}]
    }
)
print(response.json())
```

**Example** — same request, but allow wildcard and catch-all fallback:

```python
import requests

response = requests.post(
    "http://<HEAD_NODE_IP>:8092/v1/service/llm/v1/chat/completions",
    headers={
        "Authorization": "Bearer test-token",
        "Content-Type": "application/json",
        "X-Otela-Fallback": "2"
    },
    json={
        "model": "Qwen/Qwen3-8B",
        "messages": [{"role": "user", "content": "Hello!"}]
    }
)
print(response.json())
```

**Response headers**: The response includes an `X-Computing-Node` header with the Peer ID of the worker that handled the request.

**Error responses**:

- `400` — no providers found for the given service name
- `503` — providers exist but none match the request's identity group

### P2P Forward — `/v1/p2p/:peerId/*path`

Forwards the request directly to a specific peer by its Peer ID, bypassing identity group matching entirely. Useful for debugging or when you want deterministic routing.

**Supported methods**: `GET`, `POST`, `PATCH`

**URL parameters**:

- `:peerId` — the target peer's ID (e.g., `QmPneGvHmWMngc8BboFasEJQ7D2aN9C65iMDwgCRGaTazs`)
- `*path` — the path to forward to on that peer

**Example** — send a request directly to a known worker:

```python
import requests

response = requests.post(
    "http://<HEAD_NODE_IP>:8092/v1/p2p/<WORKER_PEER_ID>/v1/_service/llm/v1/chat/completions",
    headers={
        "Authorization": "Bearer test-token",
        "Content-Type": "application/json"
    },
    json={
        "model": "Qwen/Qwen3-8B",
        "messages": [{"role": "user", "content": "Hello!"}]
    }
)
print(response.json())
```

### Local Service Forward — `/v1/_service/:service/*path`

This is an **internal** endpoint. When a worker node receives a forwarded request (via P2P or global service routing), it uses this handler to proxy the request to the locally running service process (e.g., vLLM on `localhost:8080`).

You generally don't call this directly from outside the cluster. It is automatically invoked as part of the two-hop forwarding chain described above.

**Supported methods**: `GET`, `POST`, `PATCH`

## Configuring identity groups

For LLM services, identity groups are configured **automatically**. When a worker registers its `llm` service, OpenTela queries the serving engine's `/v1/models` endpoint and creates one `model=<model_id>` entry per available model.

You can also use the special values to control routing behavior:

- **`all`** — makes a worker accept every request for a service, regardless of what's in the body. Use this for catch-all or fallback nodes. This has the **lowest** routing priority and is only reached when the caller sets `X-Otela-Fallback: 2`.
- **`key=*`** — makes a worker accept any request that contains the given key, regardless of its value. Use this for nodes that can dynamically handle any variant. This has **medium** priority and is only reached when the caller sets `X-Otela-Fallback` to `1` or `2`.

Because of the priority system, you can safely combine specific and generic workers in the same cluster. Exact-match workers will always be preferred, with wildcard and catch-all workers acting as opt-in fallbacks controlled by the `X-Otela-Fallback` header.