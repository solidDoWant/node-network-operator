package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/solidDoWant/node-network-operator/test/utils"
)

// namespace where the project is deployed in
const namespace = "node-network-operator-system"

// serviceAccountName created for the project
const serviceAccountName = "node-network-operator-controller-manager"

// metricsRoleBindingName is the name of the RBAC that will be created to allow get the metrics data
const metricsRoleBindingName = "node-network-operator-metrics-binding"

var _ = Describe("Manager", Ordered, func() {

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	When("Installed via manifests/make deploy", func() {
		setupTeardown()

		BeforeAll(func() {
			By("deploying the controller-manager")
			cmd := exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", projectImage))
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")

			DeferCleanup(func() {
				By("undeploying the controller-manager")
				cmd = exec.Command("make", "undeploy")
				_, _ = utils.Run(cmd)
			})
		})

		It("should provision the webhook cert", func() {
			// Without this the pod will not start
			By("validating that cert-manager has created the certificate Secret")
			verifyCertManager := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "secrets", "webhook-server-cert", "-n", namespace)
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
			}
			Eventually(verifyCertManager).Should(Succeed())
		})

		managerTests(true)
	})

	When("Installed via Helm", func() {
		setupTeardown()

		BeforeAll(func() {
			By("deploying a self-signed issuer for cert-manager")
			Expect(utils.InstallSelfSignedIssuer()).To(Succeed(), "Failed to install self-signed issuer")
			DeferCleanup(func() {
				By("removing the self-signed issuer for cert-manager")
				utils.UninstallSelfSignedIssuer()
			})

			By("deploying the controller-manager HELM SETUP")
			projectImageRepo, projectImageTag, found := strings.Cut(projectImage, ":")
			Expect(found).To(BeTrue(), "Project image tag not found in image name")

			chartName := "node-network-operator"

			cmd := exec.Command(
				"helm", "install", chartName, "./deployment/charts/node-network-operator",
				"--namespace", namespace,
				"--set", "config.image.repository="+projectImageRepo,
				"--set", "config.image.tag="+projectImageTag,
				"--set", "config.webhook.issuerRef.kind=ClusterIssuer",
				"--set", "config.webhook.issuerRef.name=self-signed",
			)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")

			DeferCleanup(func() {
				By("undeploying the controller-manager")
				_, err := utils.Run(exec.Command("helm", "uninstall", chartName, "--namespace", namespace))
				Expect(err).NotTo(HaveOccurred(), "Failed to undeploy the controller-manager")
			})
		})

		managerTests(false)
	})
})

// setupTeardown sets up and tears down the test environment that is common to all installation methods
func setupTeardown() {
	GinkgoHelper()

	// Before running the tests, set up the environment by creating the namespace,
	// enforce the restricted security policy to the namespace, installing CRDs,
	// and deploying the controller.
	BeforeAll(func() {
		By("creating manager namespace")
		cmd := exec.Command("kubectl", "create", "ns", namespace)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")

		By("labeling the namespace with privileged security policy to allow for running in the host network namespace")
		cmd = exec.Command("kubectl", "label", "--overwrite", "ns", namespace,
			"pod-security.kubernetes.io/enforce=privileged")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to label namespace with restricted policy")

		By("installing CRDs")
		cmd = exec.Command("make", "install")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

		// After all tests have been executed, clean up by undeploying the controller, uninstalling CRDs,
		// and deleting the namespace.
		DeferCleanup(func() {
			By("uninstalling CRDs")
			cmd = exec.Command("make", "uninstall")
			_, _ = utils.Run(cmd)

			By("removing manager namespace")
			cmd = exec.Command("kubectl", "delete", "ns", namespace)
			_, _ = utils.Run(cmd)
		})
	})

	// After each test, check for failures and collect logs, events,
	// and pod descriptions for debugging.
	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			By("Fetching controller manager pods logs")
			controllerPodNames, controllerPodNamesErr := getControllerManagerPodNames()
			if controllerPodNamesErr == nil {
				for _, controllerPodName := range controllerPodNames {
					controllerLogs, err := utils.Run(exec.Command("kubectl", "logs", controllerPodName, "-n", namespace))
					if err == nil {
						_, _ = fmt.Fprintf(GinkgoWriter, "Controller logs:\n %s", controllerLogs)
					} else {
						_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Controller logs: %s", err)
					}
				}
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get controller pod names: %s", controllerPodNamesErr)
			}

			By("Fetching Kubernetes events")
			eventsOutput, err := utils.Run(exec.Command("kubectl", "get", "events", "-n", namespace, "--sort-by=.lastTimestamp"))
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Kubernetes events:\n%s", eventsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Kubernetes events: %s", err)
			}

			By("Fetching curl-metrics logs")
			metricsOutput, err := utils.Run(exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace))
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Metrics logs:\n %s", metricsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get curl-metrics logs: %s", err)
			}

			By("Fetching controller manager pod description")
			if controllerPodNamesErr == nil {
				for _, controllerPodName := range controllerPodNames {
					podDescription, err := utils.Run(exec.Command("kubectl", "describe", "pod", controllerPodName, "-n", namespace))
					if err == nil {
						fmt.Println("Pod description:\n", podDescription)
					} else {
						fmt.Println("Failed to describe controller pod")
					}
				}
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get controller pod names: %s", controllerPodNamesErr)
			}
		}
	})
}

// managerTests contains tests that should be run against the manager regardless of installation method
func managerTests(metricsRequireSAToken bool) {
	GinkgoHelper()

	Context("Manager", func() {
		It("should run successfully", func() {
			By("validating that the controller-manager pods are running as expected")
			Eventually(func(g Gomega) {
				controllerPodNames, err := getControllerManagerPodNames()
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get controller-manager pod names")
				g.Expect(len(controllerPodNames)%2).To(Equal(0), "expected an even number of controller pods running")

				for _, controllerPodName := range controllerPodNames {
					// Validate the pod's status
					cmd := exec.Command("kubectl", "get",
						"pods", controllerPodName, "-o", "jsonpath={.status.phase}",
						"-n", namespace,
					)
					output, err := utils.Run(cmd)
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(output).To(Equal("Running"), "Incorrect controller-manager pod status")
				}
			}).Should(Succeed())
		})

		It("should ensure the metrics endpoint is serving metrics", func() {
			By("validating that the metrics service is available")
			metricsServiceCmdOut, err := utils.Run(exec.Command("kubectl", "get", "service", "-l", "app.kubernetes.io/name=node-network-operator,app.kubernetes.io/component=metrics", "-n", namespace, "-o", "name"))
			Expect(err).NotTo(HaveOccurred(), "Metrics service(s) should exist")
			metricsServiceNames := utils.GetNonEmptyLines(metricsServiceCmdOut)
			Expect(metricsServiceNames).NotTo(BeEmpty(), "No metrics services found")

			By("waiting for the metrics endpoints to be ready")
			for _, metricsServiceName := range metricsServiceNames {
				Eventually(func(g Gomega) {
					metricsServiceName := strings.TrimPrefix(metricsServiceName, "service/")
					output, err := utils.Run(exec.Command("kubectl", "get", "endpoints", metricsServiceName, "-n", namespace, "-o", "go-template={{ range .subsets }}{{ range .addresses }}{{ .ip }}{{ \"\\n\" }}{{ end }}{{ end }}"))
					g.Expect(err).NotTo(HaveOccurred())
					// Skip the "endpointslice" warning
					g.Expect(utils.GetNonEmptyLines(output)[1:]).To(HaveLen(2), "Metrics endpoints is not ready or otherwise missing")
				}).Should(Succeed())
			}

			By("verifying that the controller manager is serving the metrics server")
			Eventually(func(g Gomega) {
				controllerPodNames, err := getControllerManagerPodNames()
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get controller-manager pod names")

				for _, controllerPodName := range controllerPodNames {
					output, err := utils.Run(exec.Command("kubectl", "logs", controllerPodName, "-n", namespace))
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(output).To(ContainSubstring("controller-runtime.metrics\tServing metrics server"),
						"Metrics server not yet started")
				}
			}).Should(Succeed())

			var authHeader, saConfig string
			scheme := "http"
			if metricsRequireSAToken {
				By("creating a ClusterRoleBinding for the service account to allow access to metrics")
				cmd := exec.Command("kubectl", "create", "clusterrolebinding", metricsRoleBindingName,
					"--clusterrole=node-network-operator-metrics-reader",
					fmt.Sprintf("--serviceaccount=%s:%s", namespace, serviceAccountName),
				)
				_, err := utils.Run(cmd)
				Expect(err).NotTo(HaveOccurred(), "Failed to create ClusterRoleBinding")

				DeferCleanup(func() {
					By("deleting the ClusterRoleBinding for the service account")
					_, err := utils.Run(exec.Command("kubectl", "delete", "clusterrolebinding", metricsRoleBindingName))
					Expect(err).NotTo(HaveOccurred(), "Failed to delete ClusterRoleBinding")
				})

				By("getting the service account token")
				token, err := serviceAccountToken()
				Expect(err).NotTo(HaveOccurred())
				Expect(token).NotTo(BeEmpty())

				scheme = "https"
				authHeader = fmt.Sprintf("-H 'Authorization: Bearer %s'", token)
				saConfig = fmt.Sprintf(", %q: %q", "serviceAccount", serviceAccountName)
			}

			for _, metricsServiceName := range metricsServiceNames {
				func() {
					By("getting the service metrics port number")
					port, err := utils.Run(exec.Command("kubectl", "get", metricsServiceName, "-n", namespace, "-o", "jsonpath={.spec.ports[?(@.name==\"https-metrics\")].port}{.spec.ports[?(@.name==\"http-metrics\")].port}"))
					Expect(err).NotTo(HaveOccurred(), "Failed to get metrics service port")
					Expect(port).NotTo(BeEmpty(), "Metrics service port is empty")

					metricsServiceName := strings.TrimPrefix(metricsServiceName, "service/")

					By("creating the curl-metrics pod to access the metrics endpoint")
					cmd := exec.Command("kubectl", "run", "curl-metrics", "--restart=Never",
						"--namespace", namespace,
						"--image=curlimages/curl:latest",
						"--overrides",
						fmt.Sprintf(`{
							"spec": {
								"containers": [{
									"name": "curl",
									"image": "curlimages/curl:latest",
									"command": ["/bin/sh", "-c"],
									"args": ["curl -v -k %s %s://%s.%s.svc.cluster.local:%s/metrics"],
									"securityContext": {
										"allowPrivilegeEscalation": false,
										"capabilities": {
											"drop": ["ALL"]
										},
										"runAsNonRoot": true,
										"runAsUser": 1000,
										"seccompProfile": {
											"type": "RuntimeDefault"
										}
									}
								}]%s
							}
						}`, authHeader, scheme, metricsServiceName, namespace, port, saConfig))
					_, err = utils.Run(cmd)
					Expect(err).NotTo(HaveOccurred(), "Failed to create curl-metrics pod")

					defer func() {
						By("cleaning up the curl pod for metrics")
						_, err := utils.Run(exec.Command("kubectl", "delete", "pod", "curl-metrics", "-n", namespace))
						Expect(err).NotTo(HaveOccurred(), "Failed to delete curl-metrics pod")
					}()

					By("waiting for the curl-metrics pod to complete.")
					verifyCurlUp := func(g Gomega) {
						cmd := exec.Command("kubectl", "get", "pods", "curl-metrics",
							"-o", "jsonpath={.status.phase}",
							"-n", namespace)
						output, err := utils.Run(cmd)
						g.Expect(err).NotTo(HaveOccurred())
						g.Expect(output).To(Equal("Succeeded"), "curl pod in wrong status")
					}
					Eventually(verifyCurlUp, 5*time.Minute).Should(Succeed())

					By("getting the metrics by checking curl-metrics logs")
					metricsOutput := getMetricsOutput()
					Expect(metricsOutput).To(ContainSubstring(
						"controller_runtime_",
					))
				}()
			}
		})

		It("should have CA injection for validating webhooks", func() {
			By("checking CA injection for validating webhooks")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get",
					"validatingwebhookconfigurations.admissionregistration.k8s.io",
					"node-network-operator-validating-webhook-configuration",
					"-o", "go-template={{ range .webhooks }}{{ .clientConfig.caBundle }}{{ end }}")
				vwhOutput, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(len(vwhOutput)).To(BeNumerically(">", 10))
			}).Should(Succeed())
		})

		// +kubebuilder:scaffold:e2e-webhooks-checks

		It("can deploy a sample Link resource", func() {
			By("creating a sample Link resource")
			sampleLink := filepath.Join("config", "samples", "nodenetworkoperator_v1alpha1_link.yaml")
			_, err := utils.Run(exec.Command("kubectl", "apply", "-f", sampleLink))
			Expect(err).NotTo(HaveOccurred(), "Failed to apply sample Link resource")

			defer func() {
				By("deleting the sample Link resource")
				_, err := utils.Run(exec.Command("kubectl", "delete", "-f", sampleLink))
				Expect(err).NotTo(HaveOccurred(), "Failed to delete sample Link resource")

				// Wait for the resources to be deleted
				Eventually(func(g Gomega) {
					// Link resource
					_, err := utils.Run(exec.Command("kubectl", "get", "link", "link-sample"))
					g.Expect(err).To(HaveOccurred(), "Sample Link resource should be deleted")

					// NodeLinks resource
					// This will error when there are no items
					_, err = utils.Run(exec.Command("kubectl", "get", "nodelinks", "-o", "jsonpath={.items[0]}"))
					g.Expect(err).To(HaveOccurred(), "NodeLinks resource should be deleted")
				}).Should(Succeed())
			}()

			By("validating that the Link resource is created")
			verifyLinkCreated := func(g Gomega) {
				isReady, err := utils.Run(exec.Command("kubectl", "get", "link", "link-sample", "-o", "jsonpath={.status.conditions[?(@.type==\"Ready\")].status}"))
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get Link resource")
				g.Expect(isReady).To(Equal("True"), "Link resource should be ready")
			}
			Eventually(verifyLinkCreated).Should(Succeed())

			By("validating that the NodeLinks resource succeeds")
			Expect(utils.ForEachNode(func(node string) error {
				Eventually(func(g Gomega) {
					isReady, err := utils.Run(exec.Command("kubectl", "get", "nodelinks", node, "-o", "jsonpath={.status.conditions[?(@.type==\"Ready\")].status}"))
					g.Expect(err).NotTo(HaveOccurred(), "Failed to get NodeLinks resource")
					g.Expect(isReady).To(Equal("True"), "NodeLinks resource should be ready")

					_, err = utils.Run(exec.Command("docker", "container", "exec", node, "ip", "link", "show", "sampleBridge1"))
					g.Expect(err).NotTo(HaveOccurred(), "Failed to verify link on node")
				}).Should(Succeed())

				return nil
			})).To(Succeed())
		})

		Context("gateway-network samples", func() {
			BeforeAll(func() {
				By("adding a KIND-specific iptables rule to allow traffic to the gateway-network router pod")
				Expect(utils.ForEachNode(func(nodeName string) error {
					// TODO NOTE: The following iptables rule is needed due to the kind CNI plugin to avoid masquerading the gateway bridge traffic
					// iptables -t nat -I KIND-MASQ-AGENT 1 -s 192.168.50.0/24 -m comment --comment "multus: gateway pod network is not subject to MASQUERADE" -j RETURN
					cmd := exec.Command(
						"docker", "container", "exec", nodeName,
						"iptables",
						"-t", "nat",
						"-I", "KIND-MASQ-AGENT", "1",
						"-s", "192.168.50.0/24",
						"-m", "comment", "--comment", "multus: gateway pod network is not subject to MASQUERADE",
						"-j", "RETURN")
					_, err := utils.Run(cmd)
					return err
				})).To(Succeed(), "Failed to add iptables rule")

				DeferCleanup(func() {
					By("removing the KIND-specific iptables rule")
					Expect(utils.ForEachNode(func(nodeName string) error {
						// Remove the iptables rule added earlier
						cmd := exec.Command(
							"docker", "container", "exec", nodeName,
							"iptables",
							"-t", "nat",
							"-D", "KIND-MASQ-AGENT", "1")
						_, err := utils.Run(cmd)
						return err
					})).To(Succeed(), "Failed to remove iptables rule")
				})
			})

			gatewayNetworkNamespace := "gateway-network-sample"
			BeforeEach(func() {
				By("creating namespace for the gateway-network samples")
				cmd := exec.Command("kubectl", "create", "ns", gatewayNetworkNamespace)
				_, err := utils.Run(cmd)
				Expect(err).NotTo(HaveOccurred(), "Failed to create namespace for gateway-network samples")

				DeferCleanup(func() {
					By("deleting namespace for the gateway-network samples")
					cmd := exec.Command("kubectl", "delete", "ns", gatewayNetworkNamespace)
					_, err := utils.Run(cmd)
					Expect(err).NotTo(HaveOccurred(), "Failed to delete namespace for gateway-network samples")
				})
			})

			AfterEach(func() {
				By("validating that the client-pod is running")
				Eventually(func(g Gomega) {
					cmd := exec.Command("kubectl", "get", "pod", "client-pod", "-n", gatewayNetworkNamespace, "-o", "jsonpath={.status.phase}")
					output, err := utils.Run(cmd)
					g.Expect(err).NotTo(HaveOccurred(), "Failed to get client-pod status")
					g.Expect(output).To(Equal("Running"), "client-pod should be running")
				}, 2*time.Minute).Should(Succeed())

				By("validating that the router-pod is running")
				Eventually(func(g Gomega) {
					cmd := exec.Command("kubectl", "get", "pod", "router-pod", "-n", gatewayNetworkNamespace, "-o", "jsonpath={.status.phase}")
					output, err := utils.Run(cmd)
					g.Expect(err).NotTo(HaveOccurred(), "Failed to get router-pod status")
					g.Expect(output).To(Equal("Running"), "router-pod should be running")
				}, 2*time.Minute).Should(Succeed())

				By("validating that the client-pod can ping the router-pod")
				_, err := utils.Run(exec.Command("kubectl", "exec", "-n", gatewayNetworkNamespace, "client-pod", "--", "ping", "-c", "3", "192.168.50.1"))
				Expect(err).NotTo(HaveOccurred(), "client-pod should be able to ping the router-pod")

				By("validating that the router-pod can reach the external network")
				_, err = utils.Run(exec.Command("kubectl", "exec", "-n", gatewayNetworkNamespace, "client-pod", "--", "ping", "-c", "3", "1.1.1.1"))
				Expect(err).NotTo(HaveOccurred(), "router-pod should be able to reach the external network")
			})

			It("can be deployed in single-node configuration", func() {
				sampleDir := filepath.Join("config", "samples", "gateway-network", "single-node")
				_, err := utils.Run(exec.Command("kubectl", "apply", "-n", gatewayNetworkNamespace, "-k", sampleDir))
				Expect(err).NotTo(HaveOccurred(), "Failed to apply gateway-network sample")

				DeferCleanup(func() {
					By("deleting the gateway-network sample")
					_, err := utils.Run(exec.Command("kubectl", "delete", "-n", gatewayNetworkNamespace, "-k", sampleDir))
					Expect(err).NotTo(HaveOccurred(), "Failed to delete gateway-network sample")

					// Wait for the resources to be deleted
					Eventually(func(g Gomega) {
						// NetworkAttachmentDefinition resources
						_, err := utils.Run(exec.Command("kubectl", "get", "-n", gatewayNetworkNamespace, "net-attach-def", "gateway-network-client-pods"))
						g.Expect(err).To(HaveOccurred(), "NetworkAttachmentDefinition resource should be deleted")
						_, err = utils.Run(exec.Command("kubectl", "get", "-n", gatewayNetworkNamespace, "net-attach-def", "gateway-network-router-pod"))
						g.Expect(err).To(HaveOccurred(), "NetworkAttachmentDefinition resource should be deleted")

						// Pods
						_, err = utils.Run(exec.Command("kubectl", "get", "pod", "-n", gatewayNetworkNamespace, "client-pod"))
						g.Expect(err).To(HaveOccurred(), "Client pod should be deleted")
						_, err = utils.Run(exec.Command("kubectl", "get", "pod", "-n", gatewayNetworkNamespace, "router-pod"))
						g.Expect(err).To(HaveOccurred(), "Router pod should be deleted")
					}).Should(Succeed())
				})
			})

			It("can be deployed in multi-node configuration", func() {
				sampleDir := filepath.Join("config", "samples", "gateway-network", "multi-node")
				_, err := utils.Run(exec.Command("kubectl", "apply", "-n", gatewayNetworkNamespace, "-k", sampleDir))
				Expect(err).NotTo(HaveOccurred(), "Failed to apply gateway-network sample")

				DeferCleanup(func() {
					By("deleting the gateway-network sample")
					_, err := utils.Run(exec.Command("kubectl", "delete", "-n", gatewayNetworkNamespace, "-k", sampleDir))
					Expect(err).NotTo(HaveOccurred(), "Failed to delete gateway-network sample")

					// Wait for the resources to be deleted
					Eventually(func(g Gomega) {
						// NetworkAttachmentDefinition resources
						_, err := utils.Run(exec.Command("kubectl", "get", "-n", gatewayNetworkNamespace, "net-attach-def", "gateway-network-client-pods"))
						g.Expect(err).To(HaveOccurred(), "NetworkAttachmentDefinition resource should be deleted")
						_, err = utils.Run(exec.Command("kubectl", "get", "-n", gatewayNetworkNamespace, "net-attach-def", "gateway-network-router-pod"))
						g.Expect(err).To(HaveOccurred(), "NetworkAttachmentDefinition resource should be deleted")

						// Pods
						_, err = utils.Run(exec.Command("kubectl", "get", "pod", "-n", gatewayNetworkNamespace, "client-pod"))
						g.Expect(err).To(HaveOccurred(), "Client pod should be deleted")
						_, err = utils.Run(exec.Command("kubectl", "get", "pod", "-n", gatewayNetworkNamespace, "router-pod"))
						g.Expect(err).To(HaveOccurred(), "Router pod should be deleted")
					}).Should(Succeed())
				})
			})
		})
	})
}

func getControllerManagerPodNames() ([]string, error) {
	cmd := exec.Command("kubectl", "get",
		"pods", "-l", "app.kubernetes.io/name=node-network-operator",
		"-o", "go-template={{ range .items }}"+
			"{{ if not .metadata.deletionTimestamp }}"+
			"{{ .metadata.name }}"+
			"{{ \"\\n\" }}{{ end }}{{ end }}",
		"-n", namespace,
	)

	podOutput, err := utils.Run(cmd)
	if err != nil {
		return nil, err
	}
	return utils.GetNonEmptyLines(podOutput), nil
}

// serviceAccountToken returns a token for the specified service account in the given namespace.
// It uses the Kubernetes TokenRequest API to generate a token by directly sending a request
// and parsing the resulting token from the API response.
func serviceAccountToken() (string, error) {
	const tokenRequestRawString = `{
		"apiVersion": "authentication.k8s.io/v1",
		"kind": "TokenRequest"
	}`

	// Temporary file to store the token request
	secretName := fmt.Sprintf("%s-token-request", serviceAccountName)
	tokenRequestFile := filepath.Join("/tmp", secretName)
	err := os.WriteFile(tokenRequestFile, []byte(tokenRequestRawString), os.FileMode(0o644))
	if err != nil {
		return "", err
	}

	var out string
	verifyTokenCreation := func(g Gomega) {
		// Execute kubectl command to create the token
		cmd := exec.Command("kubectl", "create", "--raw", fmt.Sprintf(
			"/api/v1/namespaces/%s/serviceaccounts/%s/token",
			namespace,
			serviceAccountName,
		), "-f", tokenRequestFile)

		output, err := cmd.CombinedOutput()
		g.Expect(err).NotTo(HaveOccurred())

		// Parse the JSON output to extract the token
		var token tokenRequest
		err = json.Unmarshal(output, &token)
		g.Expect(err).NotTo(HaveOccurred())

		out = token.Status.Token
	}
	Eventually(verifyTokenCreation).Should(Succeed())

	return out, err
}

// getMetricsOutput retrieves and returns the logs from the curl pod used to access the metrics endpoint.
func getMetricsOutput() string {
	By("getting the curl-metrics logs")
	cmd := exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
	metricsOutput, err := utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred(), "Failed to retrieve logs from curl pod")
	Expect(metricsOutput).To(ContainSubstring("< HTTP/1.1 200 OK"))
	return metricsOutput
}

// tokenRequest is a simplified representation of the Kubernetes TokenRequest API response,
// containing only the token field that we need to extract.
type tokenRequest struct {
	Status struct {
		Token string `json:"token"`
	} `json:"status"`
}
