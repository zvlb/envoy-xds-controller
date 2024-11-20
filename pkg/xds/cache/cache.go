package cache

import (
	cachev3 "github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	resourcev3 "github.com/envoyproxy/go-control-plane/pkg/resource/v3"
	"github.com/kaasops/envoy-xds-controller/pkg/errors"
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
	ErrUnknownResourceType = errors.New("unknown resource type")
	ErrEmptyResourceName   = errors.New("empty resource name")
)

type resourcesByNodeID struct {
	nodeIDs      map[string]struct{}
	resourceName string
}

func (r *resourcesByNodeID) nodeIDsToSlice() []string {
	nodeIDs := make([]string, 0, len(r.nodeIDs))

	for nodeID := range r.nodeIDs {
		nodeIDs = append(nodeIDs, nodeID)
	}

	return nodeIDs
}

type Cache struct {
	SnapshotCache cachev3.SnapshotCache

	// List of node ID in cache. Temporary, wait PR - https://github.com/envoyproxy/go-control-plane/pull/769
	nodeIDs []string

	// key - resourceType/namespace/name
	crToNodeIDs map[string]resourcesByNodeID

	mu sync.Mutex
}

func New() *Cache {
	return &Cache{
		SnapshotCache: cachev3.NewSnapshotCache(true, cachev3.IDHash{}, nil),
		crToNodeIDs:   make(map[string]resourcesByNodeID),
	}
}

//
//func (c *Cache) Update(nodeID string, resources map[resourcev3.Type][]types.Resource) error {
//	c.mu.Lock()
//	defer c.mu.Unlock()
//
//	return c.createSnapshot(nodeID, resources)
//}
//
////func (c *Cache) Update(nodeIDs []string, resource types.Resource, crName string) error {
////	c.mu.Lock()
////	defer c.mu.Unlock()
////
////	key := buildKeyForNodeIDMapping(getResourceType(resource), crName)
////	cacheItem := resourcesByNodeID{
////		nodeIDs:      make(map[string]struct{}, len(nodeIDs)),
////		resourceName: cachev3.GetResourceName(resource),
////	}
////
////	// TODO: Mass operate. Need handle error
////	for _, nodeID := range nodeIDs {
////		if err := c.update(nodeID, resource); err != nil {
////			return err
////		}
////
////		delete(c.crToNodeIDs[key].nodeIDs, nodeID)
////		cacheItem.nodeIDs[nodeID] = struct{}{}
////	}
////
////	for excessNodeID := range c.crToNodeIDs[key].nodeIDs {
////		if err := c.delete(excessNodeID, getResourceType(resource), cachev3.GetResourceName(resource)); err != nil {
////			return err
////		}
////	}
////
////	c.crToNodeIDs[key] = cacheItem
////
////	// Recalculate all nodeIDs
////	c.calculatetAllNodeIDs()
////
////	return nil
////}
//
////func (c *Cache) update(nodeID string, resource types.Resource) error {
////	resourceName := cachev3.GetResourceName(resource)
////
////	if resourceName == "" {
////		return ErrEmptyResourceName
////	}
////
////	resourceType := getResourceType(resource)
////
////	if resourceType == "" {
////		return ErrUnknownResourceType
////	}
////
////	// Get all nodeID resources indexed by type
////	resources, version, err := c.GetResources(nodeID)
////	if err != nil {
////		return err
////	}
////
////	// Get resources by type indexed by resource name
////	updated, _, err := c.getByType(resourceType, nodeID)
////	if err != nil {
////		return err
////	}
////
////	updated[resourceName] = resource
////
////	resources[resourceType] = toSlice(updated)
////
////	version++
////
////	if err := c.createSnapshot(nodeID, resources, version); err != nil {
////		return err
////	}
////
////	return nil
////}
//
//func (c *Cache) Delete(nodeIDs []string, resourceType resourcev3.Type, crName string) error {
//	c.mu.Lock()
//	defer c.mu.Unlock()
//
//	key := buildKeyForNodeIDMapping(resourceType, crName)
//	cacheItem, ok := c.crToNodeIDs[key]
//	if !ok {
//		return nil
//	}
//
//	for _, nodeID := range nodeIDs {
//		if err := c.delete(nodeID, resourceType, cacheItem.resourceName); err != nil {
//			return err
//		}
//
//		delete(cacheItem.nodeIDs, nodeID)
//	}
//
//	if len(cacheItem.nodeIDs) == 0 {
//		delete(c.crToNodeIDs, key)
//		return nil
//	}
//	c.crToNodeIDs[key] = cacheItem
//
//	// Recalculate all nodeIDs
//	c.calculatetAllNodeIDs()
//
//	return nil
//}
//
//func (c *Cache) delete(nodeID string, resourceType resourcev3.Type, resourceName string) error {
//	if resourceName == "" {
//		return ErrEmptyResourceName
//	}
//
//	if resourceType == "" {
//		return ErrUnknownResourceType
//	}
//
//	// Get all nodeID resources indexed by type
//	resources, version, err := c.GetResources(nodeID)
//	if err != nil {
//		return nil
//	}
//
//	// Get resources by type indexed by resource name
//	updated, _, err := c.getByType(resourceType, nodeID)
//	if err != nil {
//		return err
//	}
//
//	delete(updated, resourceName)
//
//	resources[resourceType] = toSlice(updated)
//
//	version++
//
//	if err := c.createSnapshot(nodeID, resources, version); err != nil {
//		return err
//	}
//
//	// If all resources deleted, remove nodeID from xds cache
//	resourceCount, err := c.getResourceCountByNodeID(nodeID)
//	if err != nil {
//		return err
//	}
//	if resourceCount == 0 {
//		c.SnapshotCache.ClearSnapshot(nodeID)
//	}
//
//	return nil
//}
//
//func (c *Cache) GetCache() cachev3.SnapshotCache {
//	return c.SnapshotCache
//}
//
//func (c *Cache) GetResources(nodeID string) (map[resourcev3.Type][]types.Resource, int, error) {
//	version := 0
//	resources := make(map[resourcev3.Type][]types.Resource, 0)
//	for _, t := range resourceTypes {
//		resourceCache, rVersionStr, err := c.getByType(t, nodeID)
//		if err != nil {
//			return nil, 0, err
//		}
//
//		// Get max version from resources
//		if rVersionStr != "" {
//			rVersion, err := strconv.Atoi(rVersionStr)
//			if err != nil {
//				return nil, 0, err
//			}
//			if rVersion > version {
//				version = rVersion
//			}
//		}
//
//		res := make([]types.Resource, 0)
//
//		for _, r := range resourceCache {
//			res = append(res, r)
//		}
//
//		resources[t] = res
//	}
//	return resources, version, nil
//}
//
//func (c *Cache) getVersion() int, error) {
//	version := 0
//	resources := make(map[resourcev3.Type][]types.Resource, 0)
//	for _, t := range resourceTypes {
//		resourceCache, rVersionStr, err := c.getByType(t, nodeID)
//		if err != nil {
//			return nil, 0, err
//		}
//
//		// Get max version from resources
//		if rVersionStr != "" {
//			rVersion, err := strconv.Atoi(rVersionStr)
//			if err != nil {
//				return nil, 0, err
//			}
//			if rVersion > version {
//				version = rVersion
//			}
//		}
//
//		res := make([]types.Resource, 0)
//
//		for _, r := range resourceCache {
//			res = append(res, r)
//		}
//
//		resources[t] = res
//	}
//	return resources, version, nil
//}
//
//func (c *Cache) GetNodeIDs(ctx *gin.Context) []string {
//	if v, exists := ctx.Get(middlewares.AvailableNodeIDs); exists {
//		availableNodeIDs := v.(map[string]struct{})
//		nodeIDs := make([]string, 0, len(availableNodeIDs))
//		for _, nodeID := range c.nodeIDs {
//			if _, ok := availableNodeIDs[nodeID]; ok {
//				nodeIDs = append(nodeIDs, nodeID)
//			}
//		}
//		return nodeIDs
//	}
//	return c.nodeIDs
//}
//
//func (c *Cache) GetNodeIDsForCustomResource(resourceType, name string) []string {
//	cacheItem, ok := c.crToNodeIDs[buildKeyForNodeIDMapping(resourceType, name)]
//	if !ok {
//		return nil
//	}
//
//	return cacheItem.nodeIDsToSlice()
//}
//
//func (c *Cache) GetAllNodeIDs() []string {
//	return c.nodeIDs
//}
//
//// func (c *Cache) SetNodeID
//
//func (c *Cache) GetAllNodeIDsMap() map[string]struct{} {
//	nodeIDs := make(map[string]struct{}, len(c.nodeIDs))
//
//	for _, nodeID := range c.nodeIDs {
//		nodeIDs[nodeID] = struct{}{}
//	}
//
//	return nodeIDs
//}
//
//// Wait blocks if:
//// 1. There is no Listener for any NodeID.
//// 2. The number of resources in the cache has changed within 10 seconds.
//func (c *Cache) Wait() error {
//	resourceCount, err := c.getResourceCount()
//	if err != nil {
//		return err
//	}
//
//	for {
//		time.Sleep(2 * time.Second)
//		resourceCountNew, err := c.getResourceCount()
//		if err != nil {
//			return err
//		}
//		if resourceCountNew == 0 {
//			continue
//		}
//		if resourceCountNew == resourceCount {
//			break
//		}
//		resourceCount = resourceCountNew
//	}
//
//	return nil
//}
//
//func (c *Cache) getResourceCount() (int, error) {
//	resourceCount := 0
//
//	nodeIDs := c.GetAllNodeIDs()
//
//	for _, nodeID := range nodeIDs {
//		resources, _, err := c.GetResources(nodeID)
//		if err != nil {
//			return resourceCount, err
//		}
//		if len(resources[resourcev3.ListenerType]) == 0 {
//			continue
//		}
//
//		for _, resourceType := range resourceTypes {
//			resourceCount += len(resources[resourceType])
//		}
//	}
//	return resourceCount, nil
//}
//
//func (c *Cache) getResourceCountByNodeID(nodeID string) (int, error) {
//	resourceCount := 0
//
//	resources, _, err := c.GetResources(nodeID)
//	if err != nil {
//		return resourceCount, err
//	}
//
//	for _, resourceType := range resourceTypes {
//		resourceCount += len(resources[resourceType])
//	}
//
//	return resourceCount, nil
//}
//
//// GetResourceFromCache return
//func (c *Cache) getByType(resourceType resourcev3.Type, nodeID string) (map[string]types.Resource, string, error) {
//	resSnap, err := c.SnapshotCache.GetSnapshot(nodeID)
//	if err == nil {
//		if resSnap.GetResources(resourceType) == nil {
//			return make(map[string]types.Resource), resSnap.GetVersion(resourceType), nil
//		}
//		return resSnap.GetResources(resourceType), resSnap.GetVersion(resourceType), nil
//	}
//	if strings.Contains(err.Error(), "no snapshot found for node") {
//		return map[string]types.Resource{}, "", nil
//	}
//	return nil, "", err
//}
//
//func (c *Cache) createSnapshot(nodeID string, resources map[resourcev3.Type][]types.Resource) error {
//	version := c.getVersion()
//
//	snapshot, err := cachev3.NewSnapshot(strconv.Itoa(version), resources)
//
//	if err != nil {
//		return err
//	}
//
//	if err := c.SnapshotCache.SetSnapshot(context.Background(), nodeID, snapshot); err != nil {
//		return err
//	}
//	return nil
//}
//
//func (c *Cache) calculatetAllNodeIDs() {
//	uniqNodeIDsMap := make(map[string]struct{})
//
//	for _, cacheItem := range c.crToNodeIDs {
//		for nodeID := range cacheItem.nodeIDs {
//			uniqNodeIDsMap[nodeID] = struct{}{}
//		}
//	}
//
//	uniqNodeIDsSlice := make([]string, 0, len(uniqNodeIDsMap))
//
//	for nodeID, _ := range uniqNodeIDsMap {
//		uniqNodeIDsSlice = append(uniqNodeIDsSlice, nodeID)
//	}
//
//	sort.Strings(uniqNodeIDsSlice)
//
//	c.nodeIDs = uniqNodeIDsSlice
//}
//
//// GetResourceName returns the resource name for a valid xDS response type.
//func getResourceType(res types.Resource) resourcev3.Type {
//	switch res.(type) {
//	case *clusterv3.Cluster:
//		return resourcev3.ClusterType
//	case *routev3.RouteConfiguration:
//		return resourcev3.RouteType
//	case *routev3.ScopedRouteConfiguration:
//		return resourcev3.ScopedRouteType
//	case *routev3.VirtualHost:
//		return resourcev3.VirtualHostType
//	case *listenerv3.Listener:
//		return resourcev3.ListenerType
//	case *endpointv3.Endpoint:
//		return resourcev3.EndpointType
//	case *tlsv3.Secret:
//		return resourcev3.SecretType
//	case *corev3.TypedExtensionConfig:
//		return resourcev3.ExtensionConfigType
//	default:
//		return ""
//	}
//}
//
//func toSlice(resources map[string]types.Resource) []types.Resource {
//	res := make([]types.Resource, 0)
//	for _, r := range resources {
//		res = append(res, r)
//	}
//	return res
//}
//
//// func (c *Cache) CheckSnapshotCache(nodeID string) error {
//// 	snap, err := c.SnapshotCache.GetSnapshot(nodeID)
//// 	if err != nil {
//// 		return err
//// 	}
//
//// 	for _, t := range resourceTypes {
//// 		// if t == resourcev3.SecretType {
//// 		// 	continue
//// 		// }
//// 		snapRes := snap.GetResources(t)
//// 		fmt.Printf("TYPE: %s\n, Len: %+v", t, len(snapRes))
//// 		// for k, v := range snapRes {
//// 		// 	fmt.Printf("Name: %s, Resource: %+v", k, v)
//// 		// }
//// 		fmt.Println()
//// 		fmt.Println()
//// 	}
//
//// 	return nil
//// }
