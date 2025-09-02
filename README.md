# node-network-operator
Manage node network configuration via Kubernetes resources

## Description
This operator supports various Kubernetes resources for managing node network configuration. This includes:
* Network link configuration (netlink links typically managed via `ip link { add | del | set }` with iproute2). Supported link types:
    * Bridges
    * VXLAN VTEPs

Planned support:
* Firewall rules via nftables

Potential future support:
* IP address management for managed network interfaces
* Wireguard support

This is _not_ a CNI plugin. It will not provide, for example, pod networking or a load balancer implementation. It _does_ allow for 
setting up node network resources for CNIs to use. For example, this can be used to build a VXLAN overlay network between all nodes, 
and then a CNI plugin with bridge support ([such as the bridge CNI plugin](https://www.cni.dev/plugins/current/main/bridge/)).

This can also be used in conjunction with a third-party CNI to build a classical L2/L3 overlay network between specific pods. This was
built to be an alternative to the [pod-gateway](https://github.com/angelnu/pod-gateway/) project when used with the bridge CNI plugin
and [multus](https://github.com/k8snetworkplumbingwg/multus-cni). This allows for routing the traffic of specific pods through a
separate "gateway" pod, without needing to give the pods the `NET_ADMIN` capability. See [here](./config/samples/gateway-network/README.md)
for an example.

## Getting Started

### Prerequisites
- go version v1.24.0+
- docker version 17.03+.
- kubectl version v1.11.3+.
- Access to a Kubernetes v1.11.3+ cluster.

### To Deploy on the cluster
**Build and push your image to the location specified by `IMG`:**

```sh
make docker-build docker-push IMG=<some-registry>/node-network-operator:tag
```

**NOTE:** This image ought to be published in the personal registry you specified.
And it is required to have access to pull the image from the working environment.
Make sure you have the proper permission to the registry if the above commands don’t work.

**Install the CRDs into the cluster:**

```sh
make install
```

**Deploy the Manager to the cluster with the image specified by `IMG`:**

```sh
make deploy IMG=<some-registry>/node-network-operator:tag
```

> **NOTE**: If you encounter RBAC errors, you may need to grant yourself cluster-admin
privileges or be logged in as admin.

**Create instances of your solution**
You can apply the samples (examples) from the config/sample:

```sh
kubectl apply -k config/samples/
```

>**NOTE**: Ensure that the samples has default values to test it out.

### To Uninstall
**Delete the instances (CRs) from the cluster:**

```sh
kubectl delete -k config/samples/
```

**Delete the APIs(CRDs) from the cluster:**

```sh
make uninstall
```

**UnDeploy the controller from the cluster:**

```sh
make undeploy
```

## Project Distribution

Following the options to release and provide this solution to the users.

### By providing a bundle with all YAML files

1. Build the installer for the image built and published in the registry:

```sh
make build-installer IMG=<some-registry>/node-network-operator:tag
```

**NOTE:** The makefile target mentioned above generates an 'install.yaml'
file in the dist directory. This file contains all the resources built
with Kustomize, which are necessary to install this project without its
dependencies.

2. Using the installer

Users can just run 'kubectl apply -f <URL for YAML BUNDLE>' to install
the project, i.e.:

```sh
kubectl apply -f https://raw.githubusercontent.com/<org>/node-network-operator/<tag or branch>/dist/install.yaml
```

### By providing a Helm Chart

1. Build the chart using the optional helm plugin

```sh
kubebuilder edit --plugins=helm/v1-alpha
```

2. See that a chart was generated under 'dist/chart', and users
can obtain this solution from there.

**NOTE:** If you change the project, you need to update the Helm Chart
using the same command above to sync the latest changes. Furthermore,
if you create webhooks, you need to use the above command with
the '--force' flag and manually ensure that any custom configuration
previously added to 'dist/chart/values.yaml' or 'dist/chart/manager/manager.yaml'
is manually re-applied afterwards.

## Contributing
// TODO(user): Add detailed information on how you would like others to contribute to this project

**NOTE:** Run `make help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## License

