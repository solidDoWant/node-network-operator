package utils

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2" // nolint:revive,staticcheck
)

const (
	prometheusOperatorVersion = "v0.77.1"
	prometheusOperatorURL     = "https://github.com/prometheus-operator/prometheus-operator/" +
		"releases/download/%s/bundle.yaml"

	certmanagerVersion = "v1.16.3"
	certmanagerURLTmpl = "https://github.com/cert-manager/cert-manager/releases/download/%s/cert-manager.yaml"

	multusVersion = "v4.2.2"
	multusURLTmpl = "https://raw.githubusercontent.com/k8snetworkplumbingwg/multus-cni/refs/tags/%s/deployments/multus-daemonset-thick.yml"

	whereaboutsVersion = "v0.9.2"
	whereaboutsURL     = "https://raw.githubusercontent.com/k8snetworkplumbingwg/whereabouts/refs/tags/%s/doc/crds/%s"
)

var (
	whereaboutsFiles = []string{
		"whereabouts.cni.cncf.io_overlappingrangeipreservations.yaml",
		"whereabouts.cni.cncf.io_ippools.yaml",
		"daemonset-install.yaml",
	}
)

func warnError(err error) {
	_, _ = fmt.Fprintf(GinkgoWriter, "warning: %v\n", err)
}

// Run executes the provided command within this context
func Run(cmd *exec.Cmd) (string, error) {
	dir, _ := GetProjectDir()
	cmd.Dir = dir

	if err := os.Chdir(cmd.Dir); err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "chdir dir: %q\n", err)
	}

	cmd.Env = append(os.Environ(), "GO111MODULE=on")
	command := strings.Join(cmd.Args, " ")
	_, _ = fmt.Fprintf(GinkgoWriter, "running: %q\n", command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("%q failed with error %q: %w", command, string(output), err)
	}

	return string(output), nil
}

// InstallPrometheusOperator installs the prometheus Operator to be used to export the enabled metrics.
func InstallPrometheusOperator() error {
	url := fmt.Sprintf(prometheusOperatorURL, prometheusOperatorVersion)
	cmd := exec.Command("kubectl", "create", "-f", url)
	_, err := Run(cmd)
	return err
}

// UninstallPrometheusOperator uninstalls the prometheus
func UninstallPrometheusOperator() {
	url := fmt.Sprintf(prometheusOperatorURL, prometheusOperatorVersion)
	cmd := exec.Command("kubectl", "delete", "-f", url)
	if _, err := Run(cmd); err != nil {
		warnError(err)
	}
}

// IsPrometheusCRDsInstalled checks if any Prometheus CRDs are installed
// by verifying the existence of key CRDs related to Prometheus.
func IsPrometheusCRDsInstalled() bool {
	// List of common Prometheus CRDs
	prometheusCRDs := []string{
		"prometheuses.monitoring.coreos.com",
		"prometheusrules.monitoring.coreos.com",
		"prometheusagents.monitoring.coreos.com",
	}

	cmd := exec.Command("kubectl", "get", "crds", "-o", "custom-columns=NAME:.metadata.name")
	output, err := Run(cmd)
	if err != nil {
		return false
	}
	crdList := GetNonEmptyLines(output)
	for _, crd := range prometheusCRDs {
		for _, line := range crdList {
			if strings.Contains(line, crd) {
				return true
			}
		}
	}

	return false
}

// UninstallCertManager uninstalls the cert manager
func UninstallCertManager() {
	url := fmt.Sprintf(certmanagerURLTmpl, certmanagerVersion)
	cmd := exec.Command("kubectl", "delete", "-f", url)
	if _, err := Run(cmd); err != nil {
		warnError(err)
	}
}

// InstallCertManager installs the cert manager bundle.
func InstallCertManager() error {
	url := fmt.Sprintf(certmanagerURLTmpl, certmanagerVersion)
	cmd := exec.Command("kubectl", "apply", "-f", url)
	if _, err := Run(cmd); err != nil {
		return err
	}
	// Wait for cert-manager-webhook to be ready, which can take time if cert-manager
	// was re-installed after uninstalling on a cluster.
	cmd = exec.Command("kubectl", "wait", "deployment.apps/cert-manager-webhook",
		"--for", "condition=Available",
		"--namespace", "cert-manager",
		"--timeout", "5m",
	)

	_, err := Run(cmd)
	return err
}

// IsCertManagerCRDsInstalled checks if any Cert Manager CRDs are installed
// by verifying the existence of key CRDs related to Cert Manager.
func IsCertManagerCRDsInstalled() bool {
	// List of common Cert Manager CRDs
	certManagerCRDs := []string{
		"certificates.cert-manager.io",
		"issuers.cert-manager.io",
		"clusterissuers.cert-manager.io",
		"certificaterequests.cert-manager.io",
		"orders.acme.cert-manager.io",
		"challenges.acme.cert-manager.io",
	}

	// Execute the kubectl command to get all CRDs
	cmd := exec.Command("kubectl", "get", "crds")
	output, err := Run(cmd)
	if err != nil {
		return false
	}

	// Check if any of the Cert Manager CRDs are present
	crdList := GetNonEmptyLines(output)
	for _, crd := range certManagerCRDs {
		for _, line := range crdList {
			if strings.Contains(line, crd) {
				return true
			}
		}
	}

	return false
}

// UninstallMultus uninstalls the Multus CNI
func UninstallMultus() {
	url := fmt.Sprintf(multusURLTmpl, multusVersion)
	cmd := exec.Command("kubectl", "delete", "-f", url)
	if _, err := Run(cmd); err != nil {
		warnError(err)
	}
}

// InstallMultus installs the Multus CNI
func InstallMultus() error {
	url := fmt.Sprintf(multusURLTmpl, multusVersion)
	cmd := exec.Command("kubectl", "apply", "-f", url)
	if _, err := Run(cmd); err != nil {
		return err
	}
	// Wait for multus-daemonset to be ready, which can take time if multus
	// was re-installed after uninstalling on a cluster.
	cmd = exec.Command("kubectl", "rollout", "status", "daemonset.apps/kube-multus-ds",
		"--namespace", "kube-system",
		"--timeout", "5m",
	)

	_, err := Run(cmd)
	return err
}

// IsMultusCRDsInstalled checks if any Multus CRDs are installed
// by verifying the existence of key CRDs related to Multus.
func IsMultusCRDsInstalled() bool {
	// List of common Multus CRDs
	multusCRDs := []string{
		"network-attachment-definitions.k8s.cni.cncf.io",
	}

	// Execute the kubectl command to get all CRDs
	cmd := exec.Command("kubectl", "get", "crds")
	output, err := Run(cmd)
	if err != nil {
		return false
	}

	// Check if any of the Multus CRDs are present
	crdList := GetNonEmptyLines(output)
	for _, crd := range multusCRDs {
		for _, line := range crdList {
			if strings.Contains(line, crd) {
				return true
			}
		}
	}

	return false
}

// UninstallWhereabouts uninstalls the Whereabouts IPAM CNI plugin
func UninstallWhereabouts() {
	// Reverse the order of this for uninstall
	uninstallFiles := slices.Clone(whereaboutsFiles)
	slices.Reverse(uninstallFiles)

	for _, file := range uninstallFiles {
		url := fmt.Sprintf(whereaboutsURL, whereaboutsVersion, file)
		cmd := exec.Command("kubectl", "delete", "-f", url)
		if _, err := Run(cmd); err != nil {
			warnError(err)
		}
	}
}

// InstallWhereabouts installs the Whereabouts IPAM CNI plugin
func InstallWhereabouts() error {
	for _, file := range whereaboutsFiles {
		url := fmt.Sprintf(whereaboutsURL, whereaboutsVersion, file)
		cmd := exec.Command("kubectl", "apply", "-f", url)
		if _, err := Run(cmd); err != nil {
			return err
		}
	}

	// Wait for whereabouts daemonset to be ready, which can take time if whereabouts
	// was re-installed after uninstalling on a cluster.
	cmd := exec.Command("kubectl", "rollout", "status", "daemonset.apps/whereabouts",
		"--namespace", "kube-system",
		"--timeout", "5m",
	)

	_, err := Run(cmd)
	return err
}

// IsWhereaboutsCRDsInstalled checks if any Whereabouts CRDs are installed
// by verifying the existence of key CRDs related to Whereabouts.
func IsWhereaboutsCRDsInstalled() bool {
	// List of common Whereabouts CRDs
	whereaboutsCRDs := []string{
		"ippools.whereabouts.cni.cncf.io",
		"overlappingrangeipreservations.whereabouts.cni.cncf.io",
	}

	// Execute the kubectl command to get all CRDs
	cmd := exec.Command("kubectl", "get", "crds")
	output, err := Run(cmd)
	if err != nil {
		return false
	}

	// Check if any of the Whereabouts CRDs are present
	crdList := GetNonEmptyLines(output)
	for _, crd := range whereaboutsCRDs {
		for _, line := range crdList {
			if strings.Contains(line, crd) {
				return true
			}
		}
	}

	return false
}

// UninstallCNIPlugins uninstalls the basic CNI plugins
// Note that this only removes the DaemonSet, not the CNI binaries on each node.
func UninstallCNIPlugins() {
	cmd := exec.Command("kubectl", "delete", "-k", "test/utils/manifests/install-cni-plugins")
	if _, err := Run(cmd); err != nil {
		warnError(err)
	}
}

// InstallCNIPlugins installs the basic CNI plugins on all nodes
func InstallCNIPlugins() error {
	cmd := exec.Command("kubectl", "apply", "-k", "test/utils/manifests/install-cni-plugins")
	if _, err := Run(cmd); err != nil {
		return err
	}

	// Wait for the daemonset to be ready
	cmd = exec.Command("kubectl", "rollout", "status", "daemonset.apps/install-cni-plugins",
		"--namespace", "kube-system",
		"--timeout", "5m",
	)

	_, err := Run(cmd)
	return err
}

// IsCNIPluginsInstalled checks if the basic CNI plugins are installed on all nodes
// by verifying the existence of the "bridge" CNI plugin binary in each node's CNI bin directory.
func IsCNIPluginsInstalled() bool {
	return ForEachNode(func(node string) error {
		// Check if the bridge CNI plugin is installed
		_, err := Run(exec.Command("docker", "exec", node, "ls", "/opt/cni/bin/bridge"))
		return err
	}) == nil
}

// LoadImageToKindClusterWithName loads a local docker image to the kind cluster
func LoadImageToKindClusterWithName(name string) error {
	cluster := "kind"
	if v, ok := os.LookupEnv("KIND_CLUSTER"); ok {
		cluster = v
	}
	kindOptions := []string{"load", "docker-image", name, "--name", cluster}
	cmd := exec.Command("kind", kindOptions...)
	_, err := Run(cmd)
	return err
}

// GetNonEmptyLines converts given command output string into individual objects
// according to line breakers, and ignores the empty elements in it.
func GetNonEmptyLines(output string) []string {
	var res []string
	elements := strings.Split(output, "\n")
	for _, element := range elements {
		if element != "" {
			res = append(res, element)
		}
	}

	return res
}

// GetProjectDir will return the directory where the project is
func GetProjectDir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return wd, fmt.Errorf("failed to get current working directory: %w", err)
	}
	wd = strings.ReplaceAll(wd, "/test/e2e", "")
	return wd, nil
}

// UncommentCode searches for target in the file and remove the comment prefix
// of the target content. The target content may span multiple lines.
func UncommentCode(filename, target, prefix string) error {
	// false positive
	// nolint:gosec
	content, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("failed to read file %q: %w", filename, err)
	}
	strContent := string(content)

	idx := strings.Index(strContent, target)
	if idx < 0 {
		return fmt.Errorf("unable to find the code %q to be uncomment", target)
	}

	out := new(bytes.Buffer)
	_, err = out.Write(content[:idx])
	if err != nil {
		return fmt.Errorf("failed to write to output: %w", err)
	}

	scanner := bufio.NewScanner(bytes.NewBufferString(target))
	if !scanner.Scan() {
		return nil
	}
	for {
		if _, err = out.WriteString(strings.TrimPrefix(scanner.Text(), prefix)); err != nil {
			return fmt.Errorf("failed to write to output: %w", err)
		}
		// Avoid writing a newline in case the previous line was the last in target.
		if !scanner.Scan() {
			break
		}
		if _, err = out.WriteString("\n"); err != nil {
			return fmt.Errorf("failed to write to output: %w", err)
		}
	}

	if _, err = out.Write(content[idx+len(target):]); err != nil {
		return fmt.Errorf("failed to write to output: %w", err)
	}

	// false positive
	// nolint:gosec
	if err = os.WriteFile(filename, out.Bytes(), 0644); err != nil {
		return fmt.Errorf("failed to write file %q: %w", filename, err)
	}

	return nil
}

func ForEachNode(f func(nodeName string) error) error {
	cmd := exec.Command("kubectl", "get", "nodes", "-o", "name")
	output, err := Run(cmd)
	if err != nil {
		return fmt.Errorf("failed to get nodes: %w", err)
	}

	nodes := GetNonEmptyLines(output)
	for _, node := range nodes {
		node = strings.TrimPrefix(node, "node/")
		if err := f(node); err != nil {
			return fmt.Errorf("failed to execute function on node %q: %w", node, err)
		}
	}

	return nil
}
