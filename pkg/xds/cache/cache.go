package cache

import (
	"context"
	"github.com/gin-gonic/gin"
	"github.com/kaasops/envoy-xds-controller/pkg/xds/api/v1/middlewares"
	"time"

	//clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	//listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	//routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	//tlsv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	cachev3 "github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	resourcev3 "github.com/envoyproxy/go-control-plane/pkg/resource/v3"
	"strconv"
	"strings"
	"sync"
)

var (
	resourceTypes = []resourcev3.Type{
		resourcev3.EndpointType,
		resourcev3.ClusterType,
		resourcev3.RouteType,
		resourcev3.ScopedRouteType,
		resourcev3.VirtualHostType,
		resourcev3.ListenerType,
		resourcev3.SecretType,
		resourcev3.ExtensionConfigType,
	}
	//ErrUnknownResourceType = errors.New("unknown resource type")
	//ErrEmptyResourceName   = errors.New("empty resource name")
)

type resource struct {
	name     string
	resource types.Resource
}

type Cache struct {
	SnapshotCache cachev3.SnapshotCache

	// List of node ID in cache. Temporary, wait PR - https://github.com/envoyproxy/go-control-plane/pull/769
	nodeIDs []string

	mu sync.Mutex
}

func New() *Cache {
	return &Cache{
		SnapshotCache: cachev3.NewSnapshotCache(true, cachev3.IDHash{}, nil),
	}
}

func (c *Cache) Update(nodeID string, resources []types.Resource, crName string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	updateResourcesMap := c.generateResourcesMap(resources)
	cacheResourceMap, version, err := c.getResourcesMapFromCache(nodeID)
	if err != nil {
		return err
	}

	resourceForUpdate := c.generateResourcesForUpdate(cacheResourceMap, updateResourcesMap)

	version++

	if err := c.createSnapshot(nodeID, resourceForUpdate, version); err != nil {
		return err
	}

	// TODO: SHIT
	c.nodeIDs = append(c.nodeIDs, nodeID)

	return nil
}

func (c *Cache) generateResourcesMap(resources []types.Resource) map[resourcev3.Type][]resource {
	var resourcesMap map[resourcev3.Type][]resource

	for _, resourceItem := range resources {
		resourceType := getResourceType(resourceItem)
		resourceName := cachev3.GetResourceName(resourceItem)

		resourcesMap[resourceType] = append(resourcesMap[resourceType], resource{
			name:     resourceName,
			resource: resourceItem,
		})
	}

	return resourcesMap
}

func (c *Cache) generateResourcesForUpdate(oldResourcesMap, NewResourceMap map[resourcev3.Type][]resource) map[resourcev3.Type][]types.Resource {
	resourcesForUpdate := make(map[resourcev3.Type][]types.Resource)

	for resourceType, oldResources := range oldResourcesMap {
		for _, oldResource := range oldResources {
			if newResources, ok := NewResourceMap[resourceType]; ok {
				for _, newResource := range newResources {
					if oldResource.name == newResource.name {
						resourcesForUpdate[resourceType] = append(resourcesForUpdate[resourceType], newResource.resource)
						continue
					}
					resourcesForUpdate[resourceType] = append(resourcesForUpdate[resourceType], oldResource.resource)
				}
			}
		}
	}

	return resourcesForUpdate
}

func (c *Cache) createSnapshot(nodeID string, resources map[resourcev3.Type][]types.Resource, version int) error {
	snapshot, err := cachev3.NewSnapshot(strconv.Itoa(version), resources)

	if err != nil {
		return err
	}

	if err := c.SnapshotCache.SetSnapshot(context.Background(), nodeID, snapshot); err != nil {
		return err
	}
	return nil
}

func (c *Cache) getResourcesMapFromCache(nodeID string) (map[resourcev3.Type][]resource, int, error) {
	version := 0
	resources := make(map[resourcev3.Type][]resource)
	for _, t := range resourceTypes {
		resourceCache, rVersionStr, err := c.getResourcesByType(t, nodeID)
		if err != nil {
			return nil, 0, err
		}

		// Get max version from resources
		if rVersionStr != "" {
			rVersion, err := strconv.Atoi(rVersionStr)
			if err != nil {
				return nil, 0, err
			}
			if rVersion > version {
				version = rVersion
			}
		}

		var res []resource

		for _, r := range resourceCache {
			res = append(res, resource{
				name:     cachev3.GetResourceName(r),
				resource: r,
			})
		}

		resources[t] = res
	}
	return resources, version, nil
}

func (c *Cache) getResourcesByType(resourceType resourcev3.Type, nodeID string) (map[string]types.Resource, string, error) {
	resSnap, err := c.SnapshotCache.GetSnapshot(nodeID)
	if err == nil {
		if resSnap.GetResources(resourceType) == nil {
			return make(map[string]types.Resource), resSnap.GetVersion(resourceType), nil
		}
		return resSnap.GetResources(resourceType), resSnap.GetVersion(resourceType), nil
	}
	if strings.Contains(err.Error(), "no snapshot found for node") {
		return map[string]types.Resource{}, "", nil
	}
	return nil, "", err
}

func (c *Cache) GetNodeIDs(ctx *gin.Context) []string {
	if v, exists := ctx.Get(middlewares.AvailableNodeIDs); exists {
		availableNodeIDs := v.(map[string]struct{})
		nodeIDs := make([]string, 0, len(availableNodeIDs))
		for _, nodeID := range c.nodeIDs {
			if _, ok := availableNodeIDs[nodeID]; ok {
				nodeIDs = append(nodeIDs, nodeID)
			}
		}
		return nodeIDs
	}
	return c.nodeIDs
}

func (c *Cache) GetCache() cachev3.SnapshotCache {
	return c.SnapshotCache
}

// Wait blocks if:
// 1. There is no Listener for any NodeID.
// 2. The number of resources in the cache has changed within 10 seconds.
func (c *Cache) Wait() error {
	resourceCount, err := c.getResourceCount()
	if err != nil {
		return err
	}

	for {
		time.Sleep(10 * time.Second)
		resourceCountNew, err := c.getResourceCount()
		if err != nil {
			return err
		}
		if resourceCountNew == 0 {
			continue
		}
		if resourceCountNew == resourceCount {
			break
		}
		resourceCount = resourceCountNew
	}

	return nil
}

func (c *Cache) getResourceCount() (int, error) {
	resourceCount := 0

	nodeIDs := c.GetAllNodeIDs()

	for _, nodeID := range nodeIDs {
		resources, _, err := c.getResourcesMapFromCache(nodeID)
		if err != nil {
			return resourceCount, err
		}
		if len(resources[resourcev3.ListenerType]) == 0 {
			continue
		}

		for _, resourceType := range resourceTypes {
			resourceCount += len(resources[resourceType])
		}
	}
	return resourceCount, nil
}

func (c *Cache) GetAllNodeIDs() []string {
	return c.nodeIDs
}
