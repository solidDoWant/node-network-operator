package controller

import (
	"context"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/elliotchance/pie/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"

	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	nodenetworkoperatorv1alpha1 "github.com/solidDoWant/node-network-operator/api/v1alpha1"
	// +kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var (
	ctx             context.Context
	cancel          context.CancelFunc
	testEnv         *envtest.Environment
	cfg             *rest.Config
	k8sClient       client.Client
	k8sCluster      cluster.Cluster
	testNetNSHandle *netns.NsHandle
	// Unfortunately k8s explicitly disallows the use of localhost or link-local addresses.
	// Choose a small private IP range that is somewhat unlikely to conflict with other networks.
	testNetNSCIDR    = "192.168.50.0/31" // Two addresses, one for each side of the veth pair
	testNetNSAddress string
)

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Controller Suite", AroundNode(withTestNetworkNamespace))
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())

	var err error
	err = nodenetworkoperatorv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	// +kubebuilder:scaffold:scheme

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
		ControlPlane: envtest.ControlPlane{
			Etcd: &envtest.Etcd{
				// Configure the etcd server to listed on an address that is tied to the veth
				// peer interface created in the test network namespace. This allows clients
				// (such as the setup health check) to connect to etcd from the host network
				// namespace.
				URL: &url.URL{
					Scheme: "http",
					Host:   net.JoinHostPort(testNetNSAddress, strconv.Itoa(2379)),
				},
			},
			APIServer: &envtest.APIServer{
				SecureServing: envtest.SecureServing{
					ListenAddr: envtest.ListenAddr{
						Address: testNetNSAddress,
						Port:    strconv.Itoa(6443),
					},
				},
			},
		},
	}
	// testEnv.ControlPlane.APIServer.Configure().Append("advertise-address", testNetNSAddress)

	// Retrieve the first found binary directory to allow running tests from IDEs
	if getFirstFoundEnvTestBinaryDir() != "" {
		testEnv.BinaryAssetsDirectory = getFirstFoundEnvTestBinaryDir()
	}

	// cfg is defined in this file globally.
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	// Build a controller-runtime cluster with the test environment config.
	// Configure the client builder to not cache any known types. The purpose of this test is not
	// to test the caching mechanism, but rather to ensure that the controller logic works correctly.
	// Caching makes tests significantly more painful to write and maintain, and does not provide
	// any additional value in this context.
	k8sObjectTypes := pie.Values(scheme.Scheme.AllKnownTypes())
	k8sObjectInstances := make([]client.Object, 0, len(k8sObjectTypes))
	for _, t := range k8sObjectTypes {
		if k8sObjectInstance, ok := reflect.New(t).Interface().(client.Object); ok {
			k8sObjectInstances = append(k8sObjectInstances, k8sObjectInstance)
		}
	}

	k8sCluster, err = cluster.New(cfg, func(o *cluster.Options) {
		o.Scheme = scheme.Scheme
		o.Client.Cache = &client.CacheOptions{
			DisableFor: k8sObjectInstances,
		}
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sCluster).NotTo(BeNil())

	k8sClient = k8sCluster.GetClient()
	Expect(k8sClient).NotTo(BeNil())

	// Run the cluster tasks in a separate goroutine to avoid blocking the test suite.
	// This will handle things like syncing informer caches.
	go func() {
		Expect(k8sCluster.Start(ctx)).NotTo(HaveOccurred(), "Cluster should run without error")
	}()

	syncCtx, cancelSync := context.WithTimeout(ctx, 10*time.Second)
	defer cancelSync()
	Expect(k8sCluster.GetCache().WaitForCacheSync(syncCtx)).To(BeTrue(), "Cache should be synced before proceeding")

	// Save the kubeconfig to ~/.kube/test-config for debugging purposes
	Expect(os.MkdirAll(clientcmd.RecommendedConfigDir, 0755)).To(Succeed())
	clusterConfig := k8sCluster.GetConfig()

	clientCmdConfig := api.NewConfig()
	clientCmdConfig.Clusters["test-cluster"] = &api.Cluster{
		Server:                   clusterConfig.Host,
		CertificateAuthorityData: clusterConfig.CAData,
	}
	clientCmdConfig.AuthInfos["test-cluster-user"] = &api.AuthInfo{
		ClientCertificateData: clusterConfig.CertData,
		ClientKeyData:         clusterConfig.KeyData,
	}
	clientCmdConfig.Contexts["test-context"] = &api.Context{
		Cluster:  "test-cluster",
		AuthInfo: "test-cluster-user",
	}
	clientCmdConfig.CurrentContext = "test-context"

	Expect(clientcmd.WriteToFile(*clientCmdConfig, filepath.Join(clientcmd.RecommendedConfigDir, "test-config"))).To(Succeed(),
		"Failed to write kubeconfig to %s", filepath.Join(clientcmd.RecommendedConfigDir, "test-config"))
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	if cancel != nil {
		cancel()
	}
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())

	Expect(os.Remove(filepath.Join(clientcmd.RecommendedConfigDir, "test-config"))).To(Or(Succeed(), MatchError(os.IsNotExist)),
		"Failed to remove kubeconfig file %s", filepath.Join(clientcmd.RecommendedConfigDir, "test-config"))
})

// getFirstFoundEnvTestBinaryDir locates the first binary in the specified path.
// ENVTEST-based tests depend on specific binaries, usually located in paths set by
// controller-runtime. When running tests directly (e.g., via an IDE) without using
// Makefile targets, the 'BinaryAssetsDirectory' must be explicitly configured.
//
// This function streamlines the process by finding the required binaries, similar to
// setting the 'KUBEBUILDER_ASSETS' environment variable. To ensure the binaries are
// properly set up, run 'make setup-envtest' beforehand.
func getFirstFoundEnvTestBinaryDir() string {
	basePath := filepath.Join("..", "..", "bin", "k8s")
	entries, err := os.ReadDir(basePath)
	if err != nil {
		logf.Log.Error(err, "Failed to read directory", "path", basePath)
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return filepath.Join(basePath, entry.Name())
		}
	}
	return ""
}

// withTestNetworkNamespace wraps a function to run in a specific network namespace.
func withTestNetworkNamespace(ctx context.Context, f func(context.Context)) {
	// Important: namespaces are per "thread", which are basically just Linux processes. The current
	// execution context must be locked to a thread so that all statements run in the same namespace.
	// The thread is never unlocked, causing it to be thrown away when the goroutine completes. This
	// ensures that the caller's network namespace is not changed by the function, even when the
	// called function errors.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Setup the test network namespace if it doesn't exist
	if testNetNSHandle == nil || !testNetNSHandle.IsOpen() {
		threadUnsafeSetupTestNetworkNamespace()
	}

	// Switch to the test network namespace
	Expect(netns.Set(*testNetNSHandle)).To(Succeed(), "Failed to set thread's network namespace")

	GinkgoHelper()
	f(ctx)
}

// threadUnsafeSetupTestNetworkNamespace sets up a test network namespace. It should be executed exclusively
// from a single thread that starts in the host network namespace.
func threadUnsafeSetupTestNetworkNamespace() {
	// Create a veth pair to allow communication from the host to the test network namespace
	// This is needed for some of the setup logic (etcd, etc.) to work correctly.
	vethAttrs := netlink.NewLinkAttrs()
	vethAttrs.Name = "test.veth0"
	vethLink := netlink.NewVeth(vethAttrs)
	vethLink.PeerName = "test.veth1"

	// Delete existing interfaces if they exist
	// Either they both exists, or none of them exists, so only one deletion is needed.
	link, err := netlink.LinkByName(vethAttrs.Name)
	Expect(err).To(Or(Succeed(), BeAssignableToTypeOf(netlink.LinkNotFoundError{})), "Failed to get link %q by name", vethAttrs.Name)

	if link != nil {
		Expect(netlink.LinkDel(link)).To(Succeed(), "Failed to delete existing link %q", vethAttrs.Name)
	}

	// Create the veth pair in the host namespace
	Expect(netlink.LinkAdd(vethLink)).To(Succeed(), "Failed to create veth pair")

	// Configure the host namespace interface
	hostNamespaceNet, err := netlink.ParseIPNet(testNetNSCIDR)
	Expect(err).NotTo(HaveOccurred(), "Failed to parse CIDR address %q", testNetNSCIDR)

	seutpInterface := func(interfaceName string, net *net.IPNet) {
		link, err := netlink.LinkByName(interfaceName)
		Expect(err).NotTo(HaveOccurred(), "Failed to get link by name %q", interfaceName)

		// Set the link up
		Expect(netlink.LinkSetUp(link)).To(Succeed(), "Failed to set link %q up", interfaceName)

		// Add the IP address to the link
		Expect(netlink.AddrAdd(link, &netlink.Addr{IPNet: net})).To(Succeed(),
			"Failed to add address %q to link %q", net, interfaceName)
	}
	seutpInterface(vethLink.Name, hostNamespaceNet)

	// Create the new network namespace
	currentNamespace, err := netns.Get()
	Expect(err).NotTo(HaveOccurred(), "Failed to get current network namespace")

	netnsName := "controller-testing"
	// This can happen if a previous test run did not clean up properly (panic, failure, etc.)
	if checkHandle, err := netns.GetFromName(netnsName); err == nil {
		Expect(checkHandle.Close()).To(Succeed(), "Failed to close existing network namespace handle")
		Expect(netns.DeleteNamed(netnsName)).To(Succeed(), "Failed to delete existing network namespace")
	}

	newHandle, err := netns.NewNamed(netnsName)
	Expect(err).NotTo(HaveOccurred(), "Failed to create new network namespace")
	testNetNSHandle = &newHandle
	DeferCleanup(func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		Expect(netns.Set(currentNamespace)).To(Succeed(), "Failed to set thread's network namespace back to host")
		Expect(currentNamespace.Close()).To(Succeed(), "Failed to close current network namespace")
		Expect(netns.DeleteNamed(netnsName)).To(Succeed(), "Failed to delete test network namespace")
		Expect(testNetNSHandle.Close()).To(Succeed(), "Failed to close test network namespace handle")
		testNetNSHandle = nil
	})

	// Switch back to the host network namespace and move the veth interface to the new namespace
	Expect(netns.Set(currentNamespace)).To(Succeed(), "Failed to set thread's network namespace back to host")

	// Move the peer interface to the new network namespace
	peerLink, err := netlink.LinkByName(vethLink.PeerName)
	Expect(err).NotTo(HaveOccurred(), "Failed to get peer link by name %q", vethLink.PeerName)
	Expect(netlink.LinkSetNsFd(peerLink, int(newHandle))).To(Succeed(),
		"Failed to move peer link to new network namespace %q", vethLink.PeerName)

	// Set up the peer interface in the new network namespace
	Expect(netns.Set(*testNetNSHandle)).To(Succeed(), "Failed to set thread's network namespace to test network namespace")
	testNamespaceNet := &net.IPNet{
		IP:   hostNamespaceNet.IP,
		Mask: hostNamespaceNet.Mask,
	}
	testNamespaceNet.IP[len(testNamespaceNet.IP)-1] += 1 // Increment the last byte for the peer address
	seutpInterface(vethLink.PeerName, testNamespaceNet)
	testNetNSAddress = testNamespaceNet.IP.String()

	// Ensure the loopback interface is up in the test network namespace
	links, err := netlink.LinkList()
	Expect(err).NotTo(HaveOccurred(), "Failed to list links in test network namespace")

	for _, link := range links {
		linkAttrs := link.Attrs()
		if linkAttrs == nil {
			continue
		}

		if linkAttrs.Flags&net.FlagLoopback != 0 {
			Expect(netlink.LinkSetUp(link)).To(Succeed(), "Failed to set loopback interface %q up", linkAttrs.Name)
			break
		}
	}

	// Deferred cleanup of the veth peers is not needed, because one of the two will be deleted
	// when the network namespace is deleted. Because veth interfaces are exclusively pairs,
	// deleting one side of the pair will automatically delete the other side.
}
