package cache

import (
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	tlsv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	resourcev3 "github.com/envoyproxy/go-control-plane/pkg/resource/v3"
)

func buildKeyForNodeIDMapping(resourceType, name string) string {
	return resourceType + "/" + name
}

// GetResourceName returns the resource name for a valid xDS response type.
func getResourceType(res types.Resource) resourcev3.Type {
	switch res.(type) {
	case *clusterv3.Cluster:
		return resourcev3.ClusterType
	case *routev3.RouteConfiguration:
		return resourcev3.RouteType
	case *routev3.ScopedRouteConfiguration:
		return resourcev3.ScopedRouteType
	case *routev3.VirtualHost:
		return resourcev3.VirtualHostType
	case *listenerv3.Listener:
		return resourcev3.ListenerType
	case *endpointv3.Endpoint:
		return resourcev3.EndpointType
	case *tlsv3.Secret:
		return resourcev3.SecretType
	case *corev3.TypedExtensionConfig:
		return resourcev3.ExtensionConfigType
	default:
		return ""
	}
}

func toSlice(resources map[string]types.Resource) []types.Resource {
	res := make([]types.Resource, 0)
	for _, r := range resources {
		res = append(res, r)
	}
	return res
}
