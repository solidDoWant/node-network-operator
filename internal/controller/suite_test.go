package controller

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/elliotchance/pie/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	bridgeoperatorv1alpha1 "github.com/solidDoWant/bridge-operator/api/v1alpha1"
	// +kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var (
	ctx        context.Context
	cancel     context.CancelFunc
	testEnv    *envtest.Environment
	cfg        *rest.Config
	k8sClient  client.Client
	k8sCluster cluster.Cluster
)

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())

	var err error
	err = bridgeoperatorv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	// +kubebuilder:scaffold:scheme

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

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
	cancel()
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())

	Expect(os.Remove(filepath.Join(clientcmd.RecommendedConfigDir, "test-config"))).To(Succeed(),
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
