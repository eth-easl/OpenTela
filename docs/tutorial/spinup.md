# Spin up multi-LLMs serving with OpenTela

OpenTela helps you connect GPU resources into a single pool, so that you can easily coordinate and manage them. This document explains how to spin up a cluster of multiple LLMs serving with OpenTela.

## Step 1: Spin up the first node (head node)

The first node, or we call it the head node, is a standalone node that serves as the main entry point for your cluster. This node should be a node where your users can access and send requests to (for example, with a public IP address). It does not need to be a powerful machine nor have a GPU.

Once you have OpenTela installed, you can spin up the head node with the following command:

```bash
./ocf start --mode standalone --public-addr {YOUR_IP_ADDR} --seed 0
```

In the above command:
- `--mode standalone` means this node will run in standalone mode, which is suitable for the head node.
- `--public-addr {YOUR_IP_ADDR}` specifies the public IP address of this node, so that other nodes can connect to it. You should replace `{YOUR_IP_ADDR}` with the actual public IP address of your machine.
- `--seed 0` sets the random seed to 0, and in this way you have a deterministic peer ID for this node, which is useful for the head node as other nodes will need to connect to it with its peer ID.

You should see the following output:

```bash
2026-02-22T13:11:35.223+0100    INFO    cmd/start.go:19 Cleaning slate
2026-02-22T13:11:35.223+0100    INFO    protocol/key.go:43      Looking for keys under: /home/xiayao/.ocfcore/keys/id
2026-02-22T13:11:35.249+0100    INFO    common/filesystem.go:9  path does not exist: /home/xiayao/.ocfcore/ocfcore.QmafRyc9ef1KKKMfG973aApDKCEEjnhf89dZDckgUeSMbB.db. Skipping...
2026-02-22T13:11:35.249+0100    INFO    protocol/bootstrap.go:19        Bootstrap: []
2026-02-22T13:11:35.249+0100    INFO    server/server.go:21     Wallet account set to 'none', skipping wallet initialization
2026-02-22T13:11:35.249+0100    INFO    protocol/crdt.go:37     Creating CRDT store, using dbpath: /home/xiayao/.ocfcore/ocfcore.QmafRyc9ef1KKKMfG973aApDKCEEjnhf89dZDckgUeSMbB.db
2026-02-22T13:11:35.274+0100    INFO    go-ds-crdt/migrations.go:165    Migration v0 to v1 finished (0 elements affected)
2026-02-22T13:11:35.282+0100    INFO    go-ds-crdt/migrations.go:70     CRDT database format v1
2026-02-22T13:11:35.282+0100    INFO    go-ds-crdt/crdt.go:302  crdt Datastore created. Number of heads: 0. Current max-height: 0. Dirty: false
2026-02-22T13:11:35.283+0100    INFO    protocol/bootstrap.go:19        Bootstrap: []
2026-02-22T13:11:35.283+0100    INFO    protocol/crdt.go:139    Mode: standalone
2026-02-22T13:11:35.283+0100    INFO    protocol/crdt.go:140    Peer ID: QmafRyc9ef1KKKMfG973aApDKCEEjnhf89dZDckgUeSMbB
2026-02-22T13:11:35.283+0100    INFO    protocol/crdt.go:141    Listen Addr: [/ip4/10.6.1.92/tcp/43905 /ip4/127.0.0.1/tcp/43905]
2026-02-22T13:11:35.283+0100    INFO    go-ds-crdt/crdt.go:512  store is marked clean. No need to repair
2026-02-22T13:11:35.283+0100    INFO    protocol/node_table.go:252      Added wallet address as provider: jd/CLEN++/Sykv9WQeWlKPBsPLSIMp8ck9lm6syFoMI=
2026-02-22T13:11:35.283+0100    INFO    platform/gpu.go:14      Error running nvidia-smi: exec: "nvidia-smi": executable file not found in $PATH - GPU info will be unavailable (this is expected if no NVIDIA GPU is present)
2026-02-22T13:11:35.305+0100    INFO    server/tracer.go:24     AXIOM_DATASET not set, tracing disabled
^B[2026-02-22T13:11:41.495+0100 INFO    protocol/bootstrap.go:19        Bootstrap: []
```

This means your head node is up and running. You can see the peer ID of this node in the logs, which is `QmafRyc9ef1KKKMfG973aApDKCEEjnhf89dZDckgUeSMbB` in this example. You will need this peer ID to connect other nodes to this head node. You can also open the status page of this node at `http://{YOUR_IP_ADDR}:8092/v1/dnt/table`. The output should look like this:

```json
{"/QmafRyc9ef1KKKMfG973aApDKCEEjnhf89dZDckgUeSMbB": {
    "id": "QmafRyc9ef1KKKMfG973aApDKCEEjnhf89dZDckgUeSMbB",
    "latency": 0,
    "privileged": false,
    "owner": "2RBK3HE/XixU6wgEYe33iuoVktKD88zmRd6K6idWQU4=",
    "current_offering": null,
    "role": null,
    "status": "",
    "available_offering": null,
    "service": [],
    "last_seen": 1771763800,
    "version": "",
    "public_address": "140.238.223.116",
    "hardware": {
      "gpus": [],
      "host_memory": 0,
      "host_memory_bandwidth": 0,
      "host_memory_used": 0
    },
    "connected": true,
    "load": null
  }
}
```

As you can see, there is only one node in the cluster now, which is the head node itself.

## Step 2: Spin up the second node (worker node)

The second node, or we call it the `worker node`, is a node that connects to the head node and serves as a worker in your cluster. This node should be a machine with GPU resources, so it can serve LLMs and handle requests from users. We use vLLM as an example of the LLM serving framework (and assume that you have already installed it according to [vLLM docs](https://docs.vllm.ai/en/stable/getting_started/installation/gpu/#requirements), and that you have `vllm` command ready in your $PATH). Once you have OpenTela and vLLM installed, you can spin up the worker node and connect it to the head node with the following command:

```bash
./ocf start --bootstrap.addr /ip4/{YOUR_IP_ADDR}/tcp/43905/p2p/{YOUR_HEAD_NODE_PEER_ID} --subprocess "vllm serve Qwen/Qwen3-8B --max_model_len 16384 --port 8080" --service.name llm --service.port 8080 --seed 1
```

In the above command:
- `--bootstrap.addr /ip4/{YOUR_IP_ADDR}/tcp/43905/p2p/{YOUR_HEAD_NODE_PEER_ID}` specifies the address of the head node to connect to. You should replace `{YOUR_IP_ADDR}` with the actual public IP address of your head node, and replace `{YOUR_HEAD_NODE_PEER_ID}` with the actual peer ID of your head node (which you can find in the logs of your head node).
- `--subprocess "vllm serve Qwen/Qwen3-8B --max_model_len 16384 --port 8080"` specifies the command to start the LLM serving subprocess. In this example, we use vLLM to serve the Qwen3-8B model, with a maximum model length of 16384 tokens, and listen on port 8080. You can replace this command with any other command to serve your desired LLM, as long as it listens on a specific port for incoming requests.
- `--service.name llm` specifies the name of the service provided by this node, which is `llm` in this case. For LLM serving purpose, please do not modify/remove this flag, as OpenTela will use this information to route requests to the correct nodes.
- `--service.port 8080` specifies the port number of the service provided by this node, which is 8080 in this case. This should be the same port number as the one used in the subprocess command.
- `--seed 1` sets the random seed to 1, so that this worker node will randomly generate a different peer ID from the head node. You can also set it to any other number, or even remove this flag to have a random seed, as long as the peer ID of this worker node is different from the head node.

Once you run the above command, OpenTela will start the subprocess to serve the LLM, and connect this worker node to the head node. 

```bash
...
(APIServer pid=477482) INFO 02-22 12:34:07 [api_server.py:1805] vLLM API server version 0.10.1.1
(APIServer pid=477482) INFO 02-22 12:34:07 [utils.py:326] non-default args: {'model_tag': 'Qwen/Qwen3-8B', 'port': 8080, 'model': 'Qwen/Qwen3-8B', 'max_model_len': 16384}
(APIServer pid=477482) INFO 02-22 12:34:12 [__init__.py:711] Resolved architecture: Qwen3ForCausalLM
(APIServer pid=477482) `torch_dtype` is deprecated! Use `dtype` instead!
(APIServer pid=477482) INFO 02-22 12:34:12 [__init__.py:1750] Using max model len 16384
(APIServer pid=477482) INFO 02-22 12:34:13 [scheduler.py:222] Chunked prefill is enabled with max_num_batched_tokens=2048.
...
2026-02-22T12:35:03.305Z        INFO    protocol/registrar.go:101       Fetched models from LLM service: {"object":"list","data":[{"id":"Qwen/Qwen3-8B","object":"model","created":1771763703,"owned_by":"vllm","root":"Qwen/Qwen3-8B","parent":null,"max_model_len":16384,"permission":[{"id":"modelperm-cc7748ee71a74fed93304b328a66d69c","object":"model_permission","created":1771763703,"allow_create_engine":false,"allow_sampling":true,"allow_logprobs":true,"allow_search_indices":false,"allow_view":true,"allow_fine_tuning":false,"organization":"*","group":null,"is_blocking":false}]}]}
Registering LLM service: {QmPneGvHmWMngc8BboFasEJQ7D2aN9C65iMDwgCRGaTazs 0 false n0yYcyXeBWRjt6uMJM44Y3cMhJIrsxJQ59MiiUePbu4= [] []  [] [{llm {[] 0 0 0} connected localhost 8080 [model=Qwen/Qwen3-8B]}] 1771763643   {[{NVIDIA GeForce RTX 3090 24576 950}] 0 0 0} true []}
...
```

and once the worker node is successfully connected to the head node, you can open the status page of the head node again at `http://{YOUR_IP_ADDR}:8092/v1/dnt/table`, you should see both the head node and the worker node in the cluster now, and the worker node should have the `llm` service registered with the model information.

```json
{
  "/QmPneGvHmWMngc8BboFasEJQ7D2aN9C65iMDwgCRGaTazs": {
    "id": "QmPneGvHmWMngc8BboFasEJQ7D2aN9C65iMDwgCRGaTazs",
    "latency": 0,
    "privileged": false,
    "owner": "n0yYcyXeBWRjt6uMJM44Y3cMhJIrsxJQ59MiiUePbu4=",
    "current_offering": null,
    "role": null,
    "status": "",
    "available_offering": null,
    "service": [
      {
        "name": "llm",
        "hardware": {
          "gpus": null,
          "host_memory": 0,
          "host_memory_bandwidth": 0,
          "host_memory_used": 0
        },
        "status": "connected",
        "host": "localhost",
        "port": "8080",
        "identity_group": [
          "model=Qwen/Qwen3-8B"
        ]
      }
    ],
    "last_seen": 1771764988,
    "version": "",
    "public_address": "",
    "hardware": {
      "gpus": [
        {
          "name": "NVIDIA GeForce RTX 3090",
          "total_memory": 24576,
          "used_memory": 950
        }
      ],
      "host_memory": 0,
      "host_memory_bandwidth": 0,
      "host_memory_used": 0
    },
    "connected": true,
    "load": null
  },
  "/QmafRyc9ef1KKKMfG973aApDKCEEjnhf89dZDckgUeSMbB": {
    "id": "QmafRyc9ef1KKKMfG973aApDKCEEjnhf89dZDckgUeSMbB",
    "latency": 0,
    "privileged": false,
    "owner": "2RBK3HE/XixU6wgEYe33iuoVktKD88zmRd6K6idWQU4=",
    "current_offering": null,
    "role": null,
    "status": "",
    "available_offering": null,
    "service": [],
    "last_seen": 1771764985,
    "version": "",
    "public_address": "140.238.223.116",
    "hardware": {
      "gpus": [],
      "host_memory": 0,
      "host_memory_bandwidth": 0,
      "host_memory_used": 0
    },
    "connected": true,
    "load": null
  }
}
```

As you can see, there are now two nodes in the cluster: the head node with peer ID `QmafRyc9ef1KKKMfG973aApDKCEEjnhf89dZDckgUeSMbB`, and the worker node with peer ID `QmPneGvHmWMngc8BboFasEJQ7D2aN9C65iMDwgCRGaTazs`. The worker node has the `llm` service registered, and it shows the model information of the served LLM (Qwen/Qwen3-8B) in the identity group.

You can spin up more worker nodes with the same command as in Step 2 with various different LLMs served, as well as different hardware configurations or serving frameworks. Each worker node will connect to the head node and register its service information, and you can see all the nodes and their services in the status page of the head node.

## Step 3: Send requests to the cluster

Once you have the cluster of LLM serving nodes up and running, you can send requests to the cluster through the head node, and OpenTela will route the requests to the appropriate worker nodes based on the service information (particularly `identity_group`) registered by each node.

For example, if you want to send a request to the LLM service to generate text with the Qwen3-8B model above, you can send a request to the head node at `http://{YOUR_IP_ADDR}:8092/v1/` with the following JSON body:

```python
import requests

response = requests.post(
    "http://{YOUR_HEAD_NODE_IP_ADDR}:8092/v1/service/llm/v1/chat/completions",
    headers={
        "Authorization": "Bearer test-token",
        "Content-Type": "application/json"
    },
    json={
        "model": "Qwen/Qwen3-8B",
        "messages": [{"role": "user", "content": "Hello, world!"}]
    }
)
print(response.json())
```

In the above code, you should replace `{YOUR_HEAD_NODE_IP_ADDR}` with the actual IP address of your head node. The request is sent to the head node's API endpoint for the `llm` service, and it includes the model information in the JSON body. OpenTela will route this request to the worker node that serves the Qwen3-8B model (using the `identity_group` information), and you should get a response from the vLLM API server with the generated text.

```json
{"id": "chatcmpl-8a81b873db0141cd901821fef0a902c0", "object": "chat.completion", "created": 1771766516, "model": "Qwen/Qwen3-8B", "choices": [{"index": 0, "message": {"role": "assistant", "content": "<think>\nOkay, the user said "Hello, world!" so they\"re probably just testing the waters or starting a conversation. I need to respond in a friendly and welcoming manner. Let me make sure to acknowledge their greeting and invite them to ask questions or share more. I should keep it simple and positive. Maybe add an emoji to keep it light. Let me check if there\"s anything else they might need. No, just a standard response should work here.\n</think>\n\nHello! 😊 How can I assist you today? Whether you have questions, need help with something, or just want to chat, I\"m here for you!", "refusal": None, "annotations": None, "audio": None, "function_call": None, "tool_calls": [], "reasoning_content": None}, "logprobs": None, "finish_reason": "stop", "stop_reason": None}], "service_tier": None, "system_fingerprint": None, "usage": {"prompt_tokens": 12, "total_tokens": 141, "completion_tokens": 129, "prompt_tokens_details": None}, "prompt_logprobs": None, "kv_transfer_params": None}
```

If you desire to send the requests to a particular worker node instead of leveraging the built-in routing logic of OpenTela, you can also send the request directly to the worker node's API endpoint, through the head node as a proxy. For example, in the above cluster setup, you can force OpenTela to route the request the worker node with the code below:

```python
import requests
response = requests.post(
    "http://{YOUR_HEAD_NODE_IP_ADDR}:8092/v1/p2p/{YOUR_WORKER_PEER_ID}/v1/_service/llm/v1/chat/completions",
    headers={
        "Authorization": "Bearer test-token",
        "Content-Type": "application/json"
    },
    json={
        "model": "Qwen/Qwen3-8B",
        "messages": [{"role": "user", "content": "Hello, world!"}]
    }
)
print(response.json())
```

As long as the serving engine (e.g., `vLLM` or `SGLang`) is compatible with OpenAI protocol (e.g., has `/completions` and/or `/chat/completions` endpoint), OpenTela is compatible with it as well and can be used with any OpenAI-compatible client. For example, you can also use `openai` python package to send requests to the cluster like this:

```python
import openai
client = openai.OpenAI(
    base_url="http://140.238.223.116:8092/v1/service/llm/v1",
    api_key="test-token"
)

response = client.chat.completions.create(
    model="Qwen/Qwen3-8B",
    messages=[{"role": "user", "content": "Hello, world!"}]
)
print(response)
```

Congratulations! You have successfully spun up a multi-LLM serving cluster with OpenTela, and sent requests to the cluster through the head node. You can now easily manage and scale your LLM serving infrastructure with OpenTela, and serve various LLMs to your users seamlessly.
