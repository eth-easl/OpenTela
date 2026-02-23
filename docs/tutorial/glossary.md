# Glossary

* *Peer* - A peer is a participant in the peer-to-peer network. Each peer can act as both a client and a server, sharing resources and information with other peers. Read more about peers [here](https://libp2p.io/guides/peers/).

* *Peer ID* - A unique identifier assigned to each peer in the network. It is used to distinguish one peer from another and facilitate communication between them. The peer id in OpenTela is base-58 encoded (similar to [Bitcoin](https://bitcoinwiki.org/wiki/Base58#Alphabet_Base58)).

* *Peer Addressing* - We use multiaddr format to represent peer addresses, which can include various protocols and transport layers. For example, a peer address might look like `/ip4/127.0.0.1/tcp/4001/p2p/QmPneGvHmWMngc8BboFasEJQ7D2aN9C65iMDwgCRGaTazs`. Read more about multiaddr format [here](https://libp2p.io/guides/addressing/).

* *Identity Group* - An identity group is a collection of peers that can interchangeably represent the same identity. Identity Group is represented as a set of key-value pairs, such as `model=Qwen/Qwen3-8B`. In the context of LLM serving with OpenTela, an identity group could be a set of peers that are all capable of serving the same LLM model. Peers that belong to the same identity group can be treated as interchangeable when it comes to serving requests for that particular model. This allows for load balancing and redundancy, as requests can be routed to any peer within the identity group without needing to specify a particular peer ID.
