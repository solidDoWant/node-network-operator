package links

import (
	"context"

	nodenetworkoperatorv1alpha1 "github.com/solidDoWant/node-network-operator/api/v1alpha1"
)

type UnmanagedManager struct{}

var _ Manager = (*UnmanagedManager)(nil)

func NewUnmanagedManager() *UnmanagedManager {
	return &UnmanagedManager{}
}

func (m *UnmanagedManager) GetDependencies() []nodenetworkoperatorv1alpha1.LinkReference {
	return nil
}

func (m *UnmanagedManager) IsUpsertNeeded(ctx context.Context, nodeLinks *nodenetworkoperatorv1alpha1.NodeLinks, links map[string]*nodenetworkoperatorv1alpha1.Link) (bool, error) {
	// Unmanaged links are not managed by the operator, so no upsert is needed
	return false, nil
}

func (m *UnmanagedManager) Upsert(ctx context.Context, nodeLinks *nodenetworkoperatorv1alpha1.NodeLinks, links map[string]*nodenetworkoperatorv1alpha1.Link) error {
	// Unmanaged links are not managed by the operator, so no upsert is performed
	return nil
}

func (m *UnmanagedManager) IsManaged() bool {
	return false
}
