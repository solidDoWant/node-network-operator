# gateway-network

The network operator supports configuring nodes for separate pod overlay networks. This is useful for when you want
all pod traffic to flow out of a single pod (or set of pods using VRRP + conntrack syncing), such as a VPN gateway.

## single-node

This example shows how to setup a "gateway network" between two pods that are on the same node. **This example does
not use or require the network operator**. If you're running k8s on a single node and this is your use case, then
installing the network operator won't provide you any benefit.
