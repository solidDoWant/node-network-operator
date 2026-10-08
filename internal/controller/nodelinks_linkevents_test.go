package controller

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	nodenetworkoperatorv1alpha1 "github.com/solidDoWant/node-network-operator/api/v1alpha1"
)

var _ = Describe("Link event watcher", func() {
	const nodeName = "test-link-events-node"

	// startWatcher runs a watcher for the given link names until the spec ends, and returns its reconcile requests.
	startWatcher := func(subscribe linkSubscribeFunc, linkNames ...string) <-chan event.GenericEvent {
		// The watcher subscribes from another goroutine, which is not locked to this spec's network namespace.
		namespace, err := netns.Get()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(namespace.Close)

		names := &linkNameSet{}
		names.set(linkNames)
		events := make(chan event.GenericEvent, 1)
		watcher := &linkEventWatcher{
			nodeName:     nodeName,
			linkNames:    names,
			events:       events,
			subscribe:    subscribe,
			netNamespace: &namespace,
			minBackoff:   10 * time.Millisecond,
			maxBackoff:   10 * time.Millisecond,
		}

		ctx, cancel := context.WithCancel(context.Background())
		stopped := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			defer close(stopped)
			Expect(watcher.Start(ctx)).To(Succeed())
		}()
		DeferCleanup(func() {
			cancel()
			Eventually(stopped).Should(BeClosed(), "The watcher should stop when its context is cancelled")
		})

		return events
	}

	// expectRequest waits for a reconcile request, then discards any requests from the same change (one change can
	// produce several netlink events) so that they cannot satisfy a later expectation.
	expectRequest := func(events <-chan event.GenericEvent, description string) {
		var request event.GenericEvent
		Eventually(events, 5*time.Second).Should(Receive(&request), "Expected a reconcile request: %s", description)
		Expect(request.Object.GetName()).To(Equal(nodeName))

		time.Sleep(300 * time.Millisecond)
		for len(events) > 0 {
			<-events
		}
	}

	expectNoRequest := func(events <-chan event.GenericEvent, description string) {
		Consistently(events, 500*time.Millisecond).ShouldNot(Receive(), "Expected no reconcile request: %s", description)
	}

	Context("When subscribed to netlink link events", func() {
		const watchedName = "nl-ev-watch0"
		const unwatchedName = "nl-ev-other0"

		AfterEach(func() {
			for _, name := range []string{watchedName, unwatchedName} {
				if link, err := netlink.LinkByName(name); err == nil {
					_ = netlink.LinkDel(link)
				}
			}
		})

		It("should request a reconcile only when a watched link changes", func() {
			events := startWatcher(netlink.LinkSubscribeWithOptions, watchedName)
			expectRequest(events, "changes made before subscribing may have been missed")

			Expect(netlink.LinkAdd(&netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: unwatchedName}})).To(Succeed())
			expectNoRequest(events, "an unwatched link was created")

			Expect(netlink.LinkAdd(&netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: watchedName}})).To(Succeed())
			expectRequest(events, "a watched link was created")

			link, err := netlink.LinkByName(watchedName)
			Expect(err).NotTo(HaveOccurred())
			Expect(netlink.LinkSetUp(link)).To(Succeed())
			expectRequest(events, "a watched link was brought up")

			Expect(netlink.LinkDel(link)).To(Succeed())
			expectRequest(events, "a watched link was deleted")
		})

		It("should not request a reconcile when no links are watched", func() {
			events := startWatcher(netlink.LinkSubscribeWithOptions)
			Expect(netlink.LinkAdd(&netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: watchedName}})).To(Succeed())
			expectNoRequest(events, "nothing is watched")
		})
	})

	Context("When the subscription fails", func() {
		It("should resubscribe and request a reconcile after the subscription ends", func() {
			var calls atomic.Int32
			fail := make(chan struct{})
			subscribe := func(ch chan<- netlink.LinkUpdate, done <-chan struct{}, _ netlink.LinkSubscribeOptions) error {
				first := calls.Add(1) == 1
				go func() {
					select {
					case <-done:
					case <-fail:
						if !first {
							<-done
						}
					}
					// Ending the subscription simulates a receive failure, such as ENOBUFS after dropped events
					close(ch)
				}()
				return nil
			}

			events := startWatcher(subscribe, "nl-ev-watch1")
			expectRequest(events, "first subscription")

			close(fail)
			expectRequest(events, "events may have been dropped before resubscribing")
			Expect(calls.Load()).To(BeNumerically(">=", 2))
		})

		It("should retry when subscribing fails", func() {
			var calls atomic.Int32
			subscribe := func(ch chan<- netlink.LinkUpdate, done <-chan struct{}, _ netlink.LinkSubscribeOptions) error {
				if calls.Add(1) == 1 {
					return errors.New("subscription failed")
				}
				go func() {
					<-done
					close(ch)
				}()
				return nil
			}

			events := startWatcher(subscribe, "nl-ev-watch2")
			expectRequest(events, "subscribed after a retry")
			Expect(calls.Load()).To(BeNumerically(">=", 2))
		})
	})

	Context("When reconciling a NodeLinks resource", func() {
		const linkName = "test-link-events-bridge"
		const interfaceName = "nl-test-br3"

		ctx := context.Background()
		request := reconcile.Request{NamespacedName: types.NamespacedName{Name: nodeName}}

		AfterEach(func() {
			if link, err := netlink.LinkByName(interfaceName); err == nil {
				_ = netlink.LinkDel(link)
			}

			var nodeLinks nodenetworkoperatorv1alpha1.NodeLinks
			if err := k8sClient.Get(ctx, request.NamespacedName, &nodeLinks); err == nil {
				nodeLinks.Finalizers = nil
				Expect(client.IgnoreNotFound(k8sClient.Update(ctx, &nodeLinks))).To(Succeed())
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &nodeLinks))).To(Succeed())
			}
			link := &nodenetworkoperatorv1alpha1.Link{ObjectMeta: metav1.ObjectMeta{Name: linkName}}
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, link))).To(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: nodeName}}))).To(Succeed())
		})

		It("should watch the links on the node until the NodeLinks is deleted", func() {
			Expect(k8sClient.Create(ctx, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: nodeName}})).To(Succeed())
			Expect(k8sClient.Create(ctx, &nodenetworkoperatorv1alpha1.Link{
				ObjectMeta: metav1.ObjectMeta{Name: linkName},
				Spec: nodenetworkoperatorv1alpha1.LinkSpec{
					LinkName:  interfaceName,
					LinkSpecs: nodenetworkoperatorv1alpha1.LinkSpecs{Bridge: &nodenetworkoperatorv1alpha1.BridgeSpec{}},
				},
			})).To(Succeed())
			nodeLinks := &nodenetworkoperatorv1alpha1.NodeLinks{
				ObjectMeta: metav1.ObjectMeta{Name: nodeName},
				Spec:       nodenetworkoperatorv1alpha1.NodeLinksSpec{MatchingLinks: []string{linkName}},
			}
			Expect(k8sClient.Create(ctx, nodeLinks)).To(Succeed())

			reconciler := NewNodeLinksReconciler(k8sCluster, nodeName)
			_, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(reconciler.watchedLinkNames.has(interfaceName)).To(BeTrue())

			Expect(k8sClient.Delete(ctx, nodeLinks)).To(Succeed())
			_, err = reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(reconciler.watchedLinkNames.empty()).To(BeTrue())
		})
	})
})
