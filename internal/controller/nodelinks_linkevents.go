package controller

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-logr/logr"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	nodenetworkoperatorv1alpha1 "github.com/solidDoWant/node-network-operator/api/v1alpha1"
)

const (
	// linkEventReceiveBufferSize is the requested netlink socket receive buffer size. A larger buffer makes dropped
	// events (ENOBUFS) less likely during bursts, such as many pod interfaces changing at once. The kernel caps it
	// at net.core.rmem_max.
	linkEventReceiveBufferSize = 1 << 20

	linkEventMinBackoff = time.Second
	linkEventMaxBackoff = time.Minute
)

// linkNameSet is a concurrency-safe set of netlink link names.
type linkNameSet struct {
	mu    sync.RWMutex
	names map[string]struct{}
}

func (s *linkNameSet) set(names []string) {
	newNames := make(map[string]struct{}, len(names))
	for _, name := range names {
		newNames[name] = struct{}{}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.names = newNames
}

func (s *linkNameSet) has(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.names[name]
	return ok
}

func (s *linkNameSet) empty() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.names) == 0
}

// linkSubscribeFunc subscribes to netlink link updates. It matches netlink.LinkSubscribeWithOptions.
type linkSubscribeFunc func(ch chan<- netlink.LinkUpdate, done <-chan struct{}, options netlink.LinkSubscribeOptions) error

// linkEventWatcher subscribes to netlink link events and requests a NodeLinks reconcile when a link that the node's
// Links refer to changes. This repairs changes made outside the operator (for example, a VXLAN removed by the kernel
// along with its device) as they happen, rather than at the next periodic re-check.
//
// Netlink event delivery is not reliable: when the socket buffer overflows, events are dropped and the subscription
// ends. The watcher then resubscribes and requests a reconcile, since it cannot know what changed. Periodic re-checks
// (ResyncInterval) remain as a backstop.
type linkEventWatcher struct {
	nodeName  string
	linkNames *linkNameSet
	events    chan<- event.GenericEvent

	subscribe    linkSubscribeFunc
	netNamespace *netns.NsHandle
	minBackoff   time.Duration
	maxBackoff   time.Duration
}

var _ manager.LeaderElectionRunnable = (*linkEventWatcher)(nil)

// NeedLeaderElection implements manager.LeaderElectionRunnable. Every node watches its own links.
func (w *linkEventWatcher) NeedLeaderElection() bool {
	return false
}

// Start implements manager.Runnable. It runs until the context is cancelled.
func (w *linkEventWatcher) Start(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("link-events").WithValues("node", w.nodeName)

	backoff := w.minBackoff
	for {
		if w.watch(ctx, log) {
			backoff = w.minBackoff
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, w.maxBackoff)
	}
}

// watch subscribes to link events and handles them until the subscription ends or the context is cancelled. It
// returns whether the subscription was established.
func (w *linkEventWatcher) watch(ctx context.Context, log logr.Logger) bool {
	updates := make(chan netlink.LinkUpdate, 64)
	done := make(chan struct{})

	var stopping atomic.Bool
	options := netlink.LinkSubscribeOptions{
		Namespace: w.netNamespace,
		ErrorCallback: func(err error) {
			// Closing the subscription makes the pending receive fail, which is expected.
			if !stopping.Load() {
				log.Error(err, "netlink link subscription error")
			}
		},
		ReceiveBufferSize: linkEventReceiveBufferSize,
	}

	if err := w.subscribe(updates, done, options); err != nil {
		log.Error(err, "failed to subscribe to netlink link events")
		return false
	}
	defer func() {
		stopping.Store(true)
		close(done)
		// The subscription goroutine closes the channel when it exits, and blocks if an update is not received.
		go func() {
			for range updates {
			}
		}()
	}()

	log.V(1).Info("subscribed to netlink link events")

	// Changes made while not subscribed were missed.
	w.requestReconcile()

	for {
		select {
		case <-ctx.Done():
			return true
		case update, ok := <-updates:
			if !ok {
				log.Info("netlink link subscription ended, resubscribing")
				return true
			}

			if attrs := update.Attrs(); attrs != nil && w.linkNames.has(attrs.Name) {
				log.V(1).Info("watched link changed", "link", attrs.Name, "messageType", update.Header.Type)
				w.requestReconcile()
			}
		}
	}
}

// requestReconcile requests a reconcile of the node's NodeLinks. If a request is already pending, it covers this
// change too, so the request is dropped.
func (w *linkEventWatcher) requestReconcile() {
	// Nothing on the node to repair, for example before the first reconcile.
	if w.linkNames.empty() {
		return
	}

	nodeLinks := &nodenetworkoperatorv1alpha1.NodeLinks{ObjectMeta: metav1.ObjectMeta{Name: w.nodeName}}
	select {
	case w.events <- event.GenericEvent{Object: nodeLinks}:
	default:
	}
}
