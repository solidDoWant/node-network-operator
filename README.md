# node-network-operator
Manage node network configuration via Kubernetes resources

## Description
This operator supports various Kubernetes resources for managing node network configuration. This includes:
* Network link configuration (netlink links typically managed via `ip link { add | del | set }` with iproute2). Supported link types:
    * Bridges
    * VXLAN VTEPs

Potential future support:
* Firewall rules via nftables
* IP address management for managed network interfaces
* Wireguard support
* Additional interface types

If you'd like a specific feature, file an issue. I'll probably implement it if it is within the scope of this project.

This is _not_ a CNI plugin. It will not provide, for example, pod networking or a load balancer implementation. It _does_ allow for 
setting up node network resources for CNIs to use. For example, this can be used to build a VXLAN overlay network between all nodes, 
and then a CNI plugin with bridge support ([such as the bridge CNI plugin](https://www.cni.dev/plugins/current/main/bridge/)).

This can also be used in conjunction with a third-party CNI to build a classical L2/L3 overlay network between specific pods. This was
built to be an alternative to the [pod-gateway](https://github.com/angelnu/pod-gateway/) project when used with the bridge CNI plugin
and [multus](https://github.com/k8snetworkplumbingwg/multus-cni). This allows for routing the traffic of specific pods through a
separate "gateway" pod, without needing to give the pods the `NET_ADMIN` capability. See [here](./config/samples/gateway-network/README.md)
for an example.

## Getting Started

The operator can be deployed via the Helm chart [here](./deploy/charts/node-network-operator/), or via Kustomize [here](./config/default/).
While both will work, the Helm chart is much more flexible, and has a slightly more robust architecture that can tolerate more failure
modes. The Helm chart is based off of [bjw-s' common library](https://github.com/bjw-s-labs/helm-charts/tree/main/charts/library/common),
allowing practically all resource fields to be managed via Helm, if needed.

See [here](./config/samples/) for a example usages. The [gateway-network multi-node example](.//config/samples/gateway-network/multi-node/)
shows how to build a dedicated overlay network between specific pods running on different nodes, with all external traffic flowing out of
a router pod. This is a good starting point for routing pod traffic through a VPN link that is managed by a specific pod.
