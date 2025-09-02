# gateway-network

The network operator supports configuring nodes for separate pod overlay networks. This is useful for when you want
all pod traffic to flow out of a single pod (or set of pods using VRRP + conntrack syncing), such as a VPN gateway.

Be aware that some "primary" CNIs, such as kindnet, configure iptables rules in such a way that all non-primary CNI
traffic coming out of a pod is SNAT'ed. If this is the case, the iptables rules on each node will need to be
updated to allow this traffic without SNATing or masquerading. Here's the rule that the e2e tests use to work around
this for kindnet:

`iptables -t nat -I KIND-MASQ-AGENT 1 -s 192.168.50.0/24 -m comment --comment "multus: gateway pod network is not subject to MASQUERADE" -j RETURN`

## Dependencies

These examples require the following to be pre-installed in the cluster. Without them, some resources may not deploy,
or the pods will not start:
* The [bridge CNI plugin](https://www.cni.dev/plugins/current/main/bridge/) - this allows pods to connect to created
  bridges
* [multus](https://github.com/k8snetworkplumbingwg/multus-cni) - this allows for using the bridge CNI in conjunction
  with the "primary" CNI
* [whereabouts](https://github.com/k8snetworkplumbingwg/whereabouts) - this allows for IP address management across
  mutliple nodes without DHCP

## single-node

This example shows how to setup a "gateway network" between two pods that are on the same node. **This example does
not use or require the network operator**. If you're running k8s on a single node and this is your use case, then
installing the network operator won't provide you any benefit.

![single-node network diagram](./docs/single-node.svg)

In this example, traffic generated in the client pod container exits the `net1` interface, destined for the router
pod `net1` interface. The router pod receives the traffic, masquerades (SNATs) the traffic, and sends it out via
`eth0` interface. While this provides no direct benefit over just sending the traffic out of the client pod `eth0`
interface, it demonstrates how the router pod could be used as an egress gateway. This gateway could send traffic to:
* The primary CNI plugin
* Another network, such as an untrusted VLAN, potentially filtering/firewalling traffic first
* A VPN tunnel

## multi-node

This example shows how to setup a "gateway network" between two pods that may be on different nodes. **This example
requires the use of the network operator**. The operator deploys a bridge to each node for pods to connect to, and
then connects the bridges with a VXLAN. The VXLAN uses a multicast group over the host's `eth0` interface to send
BUM traffic, making it scalable to multiple nodes, as long as multicast traffic can be routed between the nodes.

Implementation notes:
* This example uses a multicast address (`224.0.0.88`) within a range that is limited to the local. If
nodes span multiple subnets, then this will need to be adjusted.
* When the MTU of `Link` resources is not set, netlink is smart enough to figure out the appropriate value. IPv4
  VXLANs have an overhead of 50 bytes, so have a MTU that is 50 less than the device carrying VXLAN BUM traffic. For
  a carrier link with a standard 1500 byte MTU, netlink will set the VXLAN VTEP MTU to 1450 bytes, as well as the
  attached bridge.

  The bridge CNI plugin is not this smart. Unless otherwise specified, the bridge MTU will be set to 1500 bytes. As
  a result, packets will be fragmented (or dropped if the DF bit is set) when traversing between any two pods.

  It is recommended to ensure that all bridge CNI plugin veth interfaces are configured with a MTU less than or equal
  to the VXLAN MTU. This can still be 1500, but raising the VXLAN MTU also requires raising the carrier network's
  minimum MTU.

  Lastly, it is recommended that VXLAN's MTU is less than or equal to the next network hop's MTU (in this case, `eth0`
  within the pod namespace) so that fragmentation does not occur _after_ existing the VXLAN.

![multi-node network diagram](./docs/multi-node.svg)

The multi-node example is nearly identical to the single-node example, except the client and router pods are on
different nodes. As before, each pod connects to a network bridge in the host namespace. The bridges are then
connected via a VXLAN for BUM traffic forwarding. By using a multicast group for the VXLAN instead of unicast peers,
any number of nodes can join the VXLAN, provided that all nodes can send and receive multicast traffic.
