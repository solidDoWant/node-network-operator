package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/solidDoWant/node-network-operator/test/utils"
)

var (
	// Optional Environment Variables:
	// - CERT_MANAGER_INSTALL_SKIP=true: Skips CertManager installation during test setup.
	// - MULTUS_INSTALL_SKIP=true: Skips multus installation during test setup.

	// These variables are useful if CertManager is already installed, avoiding
	// re-installation and conflicts.
	skipCertManagerInstall = os.Getenv("CERT_MANAGER_INSTALL_SKIP") == "true"
	// isCertManagerAlreadyInstalled will be set true when CertManager CRDs be found on the cluster
	isCertManagerAlreadyInstalled = false

	// These variables are useful if multus is already installed, avoiding
	// re-installation and conflicts.
	skipMultusInstall = os.Getenv("MULTUS_INSTALL_SKIP") == "true"
	// isMultusAlreadyInstalled will be set true when Multus CRDs be found on the cluster
	isMultusAlreadyInstalled = false

	// These variables are useful if whereabouts is already installed, avoiding
	// re-installation and conflicts.
	skipWhereaboutsInstall = os.Getenv("WHEREABOUTS_INSTALL_SKIP") == "true"
	// isWhereaboutsAlreadyInstalled will be set true when Whereabouts CRDs be found on the cluster
	isWhereaboutsAlreadyInstalled = false

	// These variables are useful if the basic CNI plugins are already installed, avoiding
	// re-installation and conflicts.
	skipCNIPluginsInstall = os.Getenv("CNI_PLUGINS_INSTALL_SKIP") == "true"
	// isCNIPluginsAlreadyInstalled will be set true when basic CNI plugins DaemonSet be found on the cluster
	isCNIPluginsAlreadyInstalled = false

	// These variables are useful if the prometheus operator is already installed, avoiding
	// re-installation and conflicts.
	skipPrometheusInstall = os.Getenv("PROMETHEUS_INSTALL_SKIP") == "true"
	// isPrometheusAlreadyInstalled will be set true when Prometheus CRDs be found on the cluster
	isPrometheusAlreadyInstalled = false

	// projectImage is the name of the image which will be build and loaded
	// with the code source changes to be tested.
	projectImage = "ghcr.io/soliddowant/node-network-operator:v0.0.1"
)

// TestE2E runs the end-to-end (e2e) test suite for the project. These tests execute in an isolated,
// temporary environment to validate project changes with the purposed to be used in CI jobs.
// The default setup requires Kind, builds/loads the Manager Docker image locally, and installs
// CertManager.
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting node-network-operator integration test suite\n")
	RunSpecs(t, "e2e suite")
}

var _ = BeforeSuite(func() {
	By("ensuring that the kind cluster name is set")
	// This var is used by `LoadImageToKindClusterWithName` and other functions
	if os.Getenv("KIND_CLUSTER") == "" {
		kindClusterName, err := utils.Run(exec.Command("make", "print-kind-cluster-name"))
		Expect(err).NotTo(HaveOccurred(), "Failed to get the kind cluster name")
		Expect(kindClusterName).NotTo(BeEmpty(), "Kind cluster name should not be empty")
		os.Setenv("KIND_CLUSTER", kindClusterName)
	}

	By("building the manager(Operator) image")
	cmd := exec.Command("make", "docker-build", fmt.Sprintf("IMG=%s", projectImage))
	_, err := utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to build the manager(Operator) image")

	// TODO(user): If you want to change the e2e test vendor from Kind, ensure the image is
	// built and available before running the tests. Also, remove the following block.
	By("loading the manager(Operator) image on Kind")
	err = utils.LoadImageToKindClusterWithName(projectImage)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to load the manager(Operator) image into Kind")

	// The tests-e2e are intended to run on a temporary cluster that is created and destroyed for testing.
	// To prevent errors when tests run in environments with CertManager already installed,
	// we check for its presence before execution.
	// Setup CertManager before the suite if not skipped and if not already installed
	if !skipCertManagerInstall {
		By("checking if cert manager is installed already")
		isCertManagerAlreadyInstalled = utils.IsCertManagerCRDsInstalled()
		if !isCertManagerAlreadyInstalled {
			_, _ = fmt.Fprintf(GinkgoWriter, "Installing CertManager...\n")
			Expect(utils.InstallCertManager()).To(Succeed(), "Failed to install CertManager")
		} else {
			_, _ = fmt.Fprintf(GinkgoWriter, "WARNING: CertManager is already installed. Skipping installation...\n")
		}
	}

	if !skipMultusInstall {
		By("checking if multus is installed already")
		isMultusAlreadyInstalled = utils.IsMultusCRDsInstalled()
		if !isMultusAlreadyInstalled {
			_, _ = fmt.Fprintf(GinkgoWriter, "Installing Multus...\n")
			Expect(utils.InstallMultus()).To(Succeed(), "Failed to install Multus")
		} else {
			_, _ = fmt.Fprintf(GinkgoWriter, "WARNING: Multus is already installed. Skipping installation...\n")
		}
	}

	if !skipWhereaboutsInstall {
		By("checking if whereabouts is installed already")
		isWhereaboutsAlreadyInstalled = utils.IsWhereaboutsCRDsInstalled()
		if !isWhereaboutsAlreadyInstalled {
			_, _ = fmt.Fprintf(GinkgoWriter, "Installing Whereabouts...\n")
			Expect(utils.InstallWhereabouts()).To(Succeed(), "Failed to install Whereabouts")
		} else {
			_, _ = fmt.Fprintf(GinkgoWriter, "WARNING: Whereabouts is already installed. Skipping installation...\n")
		}
	}

	if !skipCNIPluginsInstall {
		By("checking if basic CNI plugins are installed already")
		isCNIPluginsAlreadyInstalled = utils.IsCNIPluginsInstalled()
		if !isCNIPluginsAlreadyInstalled {
			_, _ = fmt.Fprintf(GinkgoWriter, "Installing basic CNI plugins...\n")
			Expect(utils.InstallCNIPlugins()).To(Succeed(), "Failed to install basic CNI plugins")
		} else {
			_, _ = fmt.Fprintf(GinkgoWriter, "WARNING: basic CNI plugins are already installed. Skipping installation...\n")
		}
	}

	if !skipPrometheusInstall {
		By("checking if prometheus operator is installed already")
		isPrometheusAlreadyInstalled = utils.IsPrometheusCRDsInstalled()
		if !isPrometheusAlreadyInstalled {
			_, _ = fmt.Fprintf(GinkgoWriter, "Installing Prometheus Operator...\n")
			Expect(utils.InstallPrometheusOperator()).To(Succeed(), "Failed to install Prometheus Operator")
		} else {
			_, _ = fmt.Fprintf(GinkgoWriter, "WARNING: Prometheus Operator is already installed. Skipping installation...\n")
		}
	}
})

var _ = AfterSuite(func() {
	// Teardown prometheus operator after the suite if not skipped and if it was not already installed
	if !skipPrometheusInstall && !isPrometheusAlreadyInstalled {
		_, _ = fmt.Fprintf(GinkgoWriter, "Uninstalling Prometheus Operator...\n")
		utils.UninstallPrometheusOperator()
	}

	// Teardown basic CNI plugins after the suite if not skipped and if it was not already installed
	if !skipCNIPluginsInstall && !isCNIPluginsAlreadyInstalled {
		_, _ = fmt.Fprintf(GinkgoWriter, "Uninstalling basic CNI plugins...\n")
		utils.UninstallCNIPlugins()
	}

	// Teardown Whereabouts after the suite if not skipped and if it was not already installed
	if !skipWhereaboutsInstall && !isWhereaboutsAlreadyInstalled {
		_, _ = fmt.Fprintf(GinkgoWriter, "Uninstalling Whereabouts...\n")
		utils.UninstallWhereabouts()
	}

	// Teardown Multus after the suite if not skipped and if it was not already installed
	if !skipMultusInstall && !isMultusAlreadyInstalled {
		_, _ = fmt.Fprintf(GinkgoWriter, "Uninstalling Multus...\n")
		utils.UninstallMultus()
	}

	// Teardown CertManager after the suite if not skipped and if it was not already installed
	if !skipCertManagerInstall && !isCertManagerAlreadyInstalled {
		_, _ = fmt.Fprintf(GinkgoWriter, "Uninstalling CertManager...\n")
		utils.UninstallCertManager()
	}

})
