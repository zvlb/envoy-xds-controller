package builder

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"

	accesslogv3 "github.com/envoyproxy/go-control-plane/envoy/config/accesslog/v3"
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	filev3 "github.com/envoyproxy/go-control-plane/envoy/extensions/access_loggers/file/v3"
	hcmv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	tlsv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"

	"google.golang.org/protobuf/types/known/wrapperspb"

	corev1 "k8s.io/api/core/v1"

	"github.com/kaasops/envoy-xds-controller/api/v1alpha1"
	"github.com/kaasops/envoy-xds-controller/pkg/errors"
	"github.com/kaasops/envoy-xds-controller/pkg/options"
	"github.com/kaasops/envoy-xds-controller/pkg/utils/k8s"
	"google.golang.org/protobuf/types/known/anypb"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

type XdsResourceBuilder struct {
	name                    string
	virtualHost             *routev3.VirtualHost
	accessLog               *accesslogv3.AccessLog
	httpFilters             []*hcmv3.HttpFilter
	routeConfig             *routev3.RouteConfiguration
	certificatesWithDomains map[string][]string
	useRemoteAddress        *wrapperspb.BoolValue
	upgradeConfigs          []*hcmv3.HttpConnectionManager_UpgradeConfig
	usedClusters            []*clusterv3.Cluster
	usedSecrets             []*tlsv3.Secret
}

func New(
	ctx context.Context,
	client client.Client,
	vs *v1alpha1.VirtualService,
	secretsIndex map[string]corev1.Secret,
) (*XdsResourceBuilder, error) {
	resourceName := k8s.GetResourceName(vs.Namespace, vs.Name)

	accessLog, err := buildAccessLog(ctx, client, vs, resourceName)
	if err != nil {
		return nil, errors.Wrap(err, "cannot create Access Log for Virtual Service")
	}

	virtualHost, err := buildVirtualHost(ctx, client, vs)
	if err != nil {
		return nil, errors.Wrap(err, "cannot create Virtual Host for Virtual Service")
	}

	httpFilters, err := buildHttpFilters(ctx, client, vs)
	if err != nil {
		return nil, errors.Wrap(err, "cannot create HTTP Filters for Virtual Service")
	}

	routeConfig, err := buildRouteConfiguration(resourceName, virtualHost)
	if err != nil {
		return nil, errors.Wrap(err, "cannot create Route Configs for Virtual Service")
	}

	upgradeConfigs, err := buildUpgradeConfigs(vs)
	if err != nil {
		return nil, errors.Wrap(err, "cannot create Upgrade Configs for Virtual Service")
	}

	// certificatesWithDomains - map with secret name and domains if VirtualService use TLS
	var certificatesWithDomains map[string][]string
	if vs.Spec.TlsConfig != nil {
		tlsFactory := NewTlsFactory(
			vs.Spec.TlsConfig,
			vs.Namespace,
			secretsIndex,
		)

		certificatesWithDomains, err = tlsFactory.Provide(ctx, virtualHost.Domains)
		if err != nil {
			return nil, errors.Wrap(err, "cannot provide TLS certificates")
		}
	}

	xdsResourceBuilder := &XdsResourceBuilder{
		name:                    k8s.GetResourceName(vs.Namespace, vs.Name),
		virtualHost:             virtualHost,
		accessLog:               accessLog,
		httpFilters:             httpFilters,
		routeConfig:             routeConfig,
		useRemoteAddress:        useRemoteAddress(vs),
		upgradeConfigs:          upgradeConfigs,
		certificatesWithDomains: certificatesWithDomains,
	}

	err = xdsResourceBuilder.fillUsedSecrets(ctx, client)
	if err != nil {
		return nil, errors.Wrap(err, "cannot fill used secrets")
	}

	err = xdsResourceBuilder.fillUsedClusters(ctx, client)
	if err != nil {
		return nil, errors.Wrap(err, "cannot fill used clusters")
	}

	return xdsResourceBuilder, nil
}

func (xrb *XdsResourceBuilder) BuildFilterChains() ([]*listenerv3.FilterChain, error) {
	var chains []*listenerv3.FilterChain

	b := &filterChainBuilder{}

	statPrefix := strings.ReplaceAll(xrb.name, ".", "-")

	if xrb.certificatesWithDomains != nil {
		for certName, domains := range xrb.certificatesWithDomains {
			xrb.virtualHost.Domains = domains
			f, err := b.WithDownstreamTlsContext(certName).
				WithFilterChainMatch(domains).
				WithHttpConnectionManager(xrb.accessLog,
					xrb.httpFilters,
					xrb.name,
					statPrefix,
					xrb.useRemoteAddress,
					xrb.upgradeConfigs,
				).
				Build(xrb.name)
			if err != nil {
				return nil, errors.Wrap(err, "failed to generate Filter Chain")
			}
			chains = append(chains, f)
		}
		return chains, nil
	}

	f, err := b.WithHttpConnectionManager(
		xrb.accessLog,
		xrb.httpFilters,
		xrb.name,
		statPrefix,
		xrb.useRemoteAddress,
		xrb.upgradeConfigs,
	).
		WithFilterChainMatch(xrb.virtualHost.Domains).
		Build(xrb.name)
	if err != nil {
		return nil, errors.Wrap(err, "failed to generate Filter Chain")
	}
	chains = append(chains, f)

	return chains, nil
}

func (xrb *XdsResourceBuilder) GetRouteConfiguration() *routev3.RouteConfiguration {
	return xrb.routeConfig
}

func (xrb *XdsResourceBuilder) GetUsedSecrets() []*tlsv3.Secret {
	return xrb.usedSecrets
}

func (xrb *XdsResourceBuilder) GetUsedResources(
	ctx context.Context,
	cl client.Client,
	namesapce string,
) (map[v1alpha1.ResourceType][]v1alpha1.ResourceRef, error) {
	usedResources := map[v1alpha1.ResourceType][]v1alpha1.ResourceRef{}

	secrets := xrb.getUsedSecretsResourceRefs()
	clusters, err := xrb.getUsedClustersResourceRefs(ctx, cl, namesapce)
	if err != nil {
		return nil, err
	}

	// TODO: get templates, routes, accesslogs etc

	usedResources[v1alpha1.SecretType] = secrets
	usedResources[v1alpha1.ClusterType] = clusters

	return usedResources, nil
}

func (xrb *XdsResourceBuilder) getUsedSecretsResourceRefs() []v1alpha1.ResourceRef {
	var resourceRefs []v1alpha1.ResourceRef

	for _, secretV3 := range xrb.usedSecrets {
		secretV3Name := secretV3.Name
		namespace, name, err := k8s.SplitResourceName(secretV3Name)
		if err != nil {
			return nil
		}

		resourceRefs = append(resourceRefs, v1alpha1.ResourceRef{
			Namespace: &namespace,
			Name:      name,
		})
	}

	return resourceRefs
}

func (xrb *XdsResourceBuilder) getUsedClustersResourceRefs(
	ctx context.Context,
	cl client.Client,
	namespace string,
) ([]v1alpha1.ResourceRef, error) {
	var resourceRefs []v1alpha1.ResourceRef

	if namespace == "default" {
		namespace = ""
	}
	clusterList := &v1alpha1.ClusterList{}
	if err := cl.List(
		ctx,
		clusterList,
		client.InNamespace(namespace),
	); err != nil {
		return resourceRefs, err
	}

	for _, clusterV3 := range xrb.usedClusters {
		clusterV3Name := clusterV3.Name

		for _, clusterKube := range clusterList.Items {
			clusterKubeV3 := clusterv3.Cluster{}
			if err := options.Unmarshaler.Unmarshal(clusterKube.Spec.Raw, &clusterKubeV3); err != nil {
				return resourceRefs, err
			}
			if clusterKubeV3.Name == clusterV3Name {
				if clusterKube.Namespace == "" {
					clusterKube.Namespace = "default"
				}
				resourceRefs = append(resourceRefs, v1alpha1.ResourceRef{
					Namespace: &clusterKube.Namespace,
					Name:      clusterKube.Name,
				})
			}
		}
	}

	return resourceRefs, nil
}

func (xrb *XdsResourceBuilder) fillUsedSecrets(
	ctx context.Context,
	cl client.Client,
) error {
	var v3Secrets []*tlsv3.Secret

	getEnvoySecret := func(namespace, name string) ([]*tlsv3.Secret, error) {
		kubeSecret := &corev1.Secret{}
		err := cl.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, kubeSecret)
		if err != nil {
			return nil, errors.Wrap(err, "cannot get secret")
		}

		return makeEnvoySecretFromKubernetesSecret(kubeSecret)
	}

	// Get Secrets from certificatesWithDomains
	for secretName, _ := range xrb.certificatesWithDomains {
		namespace, name, err := k8s.SplitResourceName(secretName)
		if err != nil {
			return errors.Wrap(err, "cannot split certificate name")
		}

		v3Secret, err := getEnvoySecret(namespace, name)
		if err != nil {
			return errors.Wrap(err, "cannot get envoy secret")
		}

		v3Secrets = append(v3Secrets, v3Secret...)
	}

	// Get Secrets from HTTP Filters
	for _, filter := range xrb.httpFilters {
		jsonData, err := json.MarshalIndent(filter, "", "  ")
		if err != nil {
			return err
		}

		var data interface{}
		if err := json.Unmarshal(jsonData, &data); err != nil {
			return err
		}

		fieldName := "sds_config"
		secretNames := findSDSNames(data, fieldName)

		for _, secretName := range secretNames {
			namespace, name, err := k8s.SplitResourceName(secretName)
			if err != nil {
				return errors.Wrap(err, "cannot split secret name")
			}

			v3Secret, err := getEnvoySecret(namespace, name)
			if err != nil {
				return errors.Wrap(err, "cannot get envoy secret")
			}

			v3Secrets = append(v3Secrets, v3Secret...)
		}
	}

	xrb.usedSecrets = v3Secrets

	return nil
}

func findSDSNames(data interface{}, fieldName string) []string {
	var results []string

	switch value := data.(type) {
	case map[string]interface{}:
		for k, v := range value {
			if k == fieldName {
				results = append(results, fmt.Sprintf("%v", value["name"]))
			}
			results = append(results, findSDSNames(v, fieldName)...)
		}
	case []interface{}:
		for _, item := range value {
			results = append(results, findSDSNames(item, fieldName)...)
		}
	}

	return results
}

func (xrb *XdsResourceBuilder) GetUsedClusters() []*clusterv3.Cluster {
	return xrb.usedClusters
}

func (xrb *XdsResourceBuilder) fillUsedClusters(
	ctx context.Context,
	cl client.Client,
) error {
	var v3Clusters []*clusterv3.Cluster

	for _, route := range xrb.virtualHost.Routes {
		jsonData, err := json.MarshalIndent(route, "", "  ")
		if err != nil {
			return err
		}

		var data interface{}
		if err := json.Unmarshal(jsonData, &data); err != nil {
			log.Fatalf("Failed to unmarshal JSON: %v", err)
		}

		fieldName := "Cluster"
		clusterNames := findClusterNames(data, fieldName)

		for _, clusterName := range clusterNames {
			namespace, _, err := k8s.SplitResourceName(xrb.name)
			if err != nil {
				return errors.Wrap(err, "cannot split secret name")
			}

			kubeCluster, err := v1alpha1.GetClusterByxDSName(
				ctx,
				cl,
				namespace,
				clusterName,
			)
			if err != nil {
				return errors.Wrap(err, "cannot get cluster CR name")
			}

			if kubeCluster == nil {
				continue
			}

			v3Cluster := &clusterv3.Cluster{}
			if err := options.Unmarshaler.Unmarshal(kubeCluster.Spec.Raw, v3Cluster); err != nil {
				return errors.Wrap(err, errors.UnmarshalMessage)
			}

			v3Clusters = append(v3Clusters, v3Cluster)
		}
	}

	xrb.usedClusters = v3Clusters

	return nil
}

func findClusterNames(data interface{}, fieldName string) []string {
	var results []string

	switch value := data.(type) {
	case map[string]interface{}:
		for k, v := range value {
			if k == fieldName {
				results = append(results, fmt.Sprintf("%v", v))
			}
			results = append(results, findClusterNames(v, fieldName)...)
		}
	case []interface{}:
		for _, item := range value {
			results = append(results, findClusterNames(item, fieldName)...)
		}
	}

	return results
}

func buildRouteConfiguration(name string, vh *routev3.VirtualHost) (*routev3.RouteConfiguration, error) {
	routeConfig := &routev3.RouteConfiguration{
		Name: name,
		VirtualHosts: []*routev3.VirtualHost{{
			Name: name,
			// Clean domain list that tls package can split to multiply filterchains
			Domains:             []string{"*"},
			Routes:              vh.Routes,
			RequestHeadersToAdd: vh.RequestHeadersToAdd,
		}},
	}

	if err := routeConfig.ValidateAll(); err != nil {
		return nil, errors.WrapUKS(err, errors.CannotValidateCacheResourceMessage)
	}

	return routeConfig, nil
}

func buildAccessLog(
	ctx context.Context,
	cl client.Client,
	vs *v1alpha1.VirtualService,
	resName string,
) (*accesslogv3.AccessLog, error) {
	var data []byte
	accessLog := accesslogv3.AccessLog{}

	if vs.Spec.AccessLog == nil && vs.Spec.AccessLogConfig == nil {
		return nil, nil
	}

	if vs.Spec.AccessLog != nil && vs.Spec.AccessLogConfig != nil {
		return nil, errors.New(errors.MultipleAccessLogConfigMessage)
	}

	if vs.Spec.AccessLog != nil {
		data = vs.Spec.AccessLog.Raw

		if err := options.Unmarshaler.Unmarshal(data, &accessLog); err != nil {
			return nil, errors.WrapUKS(err, errors.UnmarshalMessage)
		}

		if err := accessLog.ValidateAll(); err != nil {
			return nil, errors.WrapUKS(err, errors.CannotValidateCacheResourceMessage)
		}
	}

	if vs.Spec.AccessLogConfig != nil {
		accessLogConfig := &v1alpha1.AccessLogConfig{}
		err := cl.Get(ctx, vs.Spec.AccessLogConfig.NamespacedName(vs.Namespace), accessLogConfig)
		if err != nil {
			return nil, errors.Wrap(err, errors.GetFromKubernetesMessage)
		}
		data = accessLogConfig.Spec.Raw

		if err := options.Unmarshaler.Unmarshal(data, &accessLog); err != nil {
			return nil, errors.WrapUKS(err, errors.UnmarshalMessage)
		}

		if val, ok := accessLogConfig.Annotations[options.AutoGeneratedFilename]; ok {
			val, err := strconv.ParseBool(val)
			if err != nil {
				return nil, errors.NewUKS(errors.AccessLogAutoGeneratedFilenameBool)
			}

			if val {
				fileConfig := &filev3.FileAccessLog{}
				configType, ok := accessLog.GetConfigType().(*accesslogv3.AccessLog_TypedConfig)
				if !ok {
					return nil, errors.NewUKS(errors.ConvertTypeErrorMessage)
				}

				if err := configType.TypedConfig.UnmarshalTo(fileConfig); err != nil {
					return nil, errors.WrapUKS(err, errors.AccessLogAutoGeneratedFilenameBool)
				}
				fileConfig.Path += fmt.Sprintf("/%s.log", resName)

				fileConfigAny, err := anypb.New(fileConfig)
				if err != nil {
					return nil, errors.WrapUKS(err, "failed to marshal fileConfig to anypb")
				}

				accessLog.ConfigType = &accesslogv3.AccessLog_TypedConfig{
					TypedConfig: fileConfigAny,
				}
			}
		}

		if err := accessLog.ValidateAll(); err != nil {
			return nil, errors.WrapUKS(err, errors.CannotValidateCacheResourceMessage)
		}
	}

	return &accessLog, nil
}

func buildVirtualHost(ctx context.Context, cl client.Client, vs *v1alpha1.VirtualService) (*routev3.VirtualHost, error) {
	virtualHost := &routev3.VirtualHost{}
	if err := options.Unmarshaler.Unmarshal(vs.Spec.VirtualHost.Raw, virtualHost); err != nil {
		return nil, errors.WrapUKS(err, errors.UnmarshalMessage)
	}

	// TODO: Dont get routes from cluster all the time
	if len(vs.Spec.AdditionalRoutes) != 0 {
		for _, rts := range vs.Spec.AdditionalRoutes {
			routesSpec := &v1alpha1.Route{}
			err := cl.Get(ctx, rts.NamespacedName(vs.Namespace), routesSpec)
			if err != nil {
				return nil, errors.Wrap(err, errors.GetFromKubernetesMessage)
			}
			for _, rt := range routesSpec.Spec {
				routes := &routev3.Route{}
				if err := options.Unmarshaler.Unmarshal(rt.Raw, routes); err != nil {
					return nil, errors.WrapUKS(err, errors.UnmarshalMessage)
				}
				virtualHost.Routes = append(virtualHost.Routes, routes)
			}
		}
	}

	if err := virtualHost.ValidateAll(); err != nil {
		return nil, errors.WrapUKS(err, errors.CannotValidateCacheResourceMessage)
	}

	return virtualHost, nil
}

func buildHttpFilters(ctx context.Context, cl client.Client, vs *v1alpha1.VirtualService) ([]*hcmv3.HttpFilter, error) {

	httpFilters := []*hcmv3.HttpFilter{}

	rbacFilter, err := v1alpha1.VirtualServiceRBACFilter(ctx, cl, vs)
	if err != nil {
		return nil, err
	}
	if rbacFilter != nil {
		configType := &hcmv3.HttpFilter_TypedConfig{
			TypedConfig: &anypb.Any{},
		}
		if err := configType.TypedConfig.MarshalFrom(rbacFilter); err != nil {
			return nil, err
		}
		httpFilters = append(httpFilters, &hcmv3.HttpFilter{
			Name:       "exc.filters.http.rbac",
			ConfigType: configType,
		})
	}

	for _, httpFilter := range vs.Spec.HTTPFilters {
		hf := &hcmv3.HttpFilter{}
		if err := v1alpha1.UnmarshalAndValidateHTTPFilter(httpFilter.Raw, hf); err != nil {
			return nil, err
		}
		httpFilters = append(httpFilters, hf)
	}

	// TODO: Dont get buildHttpFilters from cluster all the time
	if len(vs.Spec.AdditionalHttpFilters) != 0 {
		for _, hfs := range vs.Spec.AdditionalHttpFilters {
			hfSpec := &v1alpha1.HttpFilter{}
			err := cl.Get(ctx, hfs.NamespacedName(vs.Namespace), hfSpec)
			if err != nil {
				return nil, errors.Wrap(err, errors.GetFromKubernetesMessage)
			}
			for _, httpFilter := range hfSpec.Spec {
				hf := &hcmv3.HttpFilter{}
				if err := v1alpha1.UnmarshalAndValidateHTTPFilter(httpFilter.Raw, hf); err != nil {
					return nil, err
				}
				httpFilters = append(httpFilters, hf)
			}
		}
	}

	return httpFilters, nil
}

func useRemoteAddress(vs *v1alpha1.VirtualService) *wrapperspb.BoolValue {
	ura := wrapperspb.BoolValue{
		Value: false,
	}

	if vs.Spec.UseRemoteAddress != nil {
		ura = wrapperspb.BoolValue{
			Value: *vs.Spec.UseRemoteAddress,
		}
	}

	return &ura
}

func buildUpgradeConfigs(vs *v1alpha1.VirtualService) ([]*hcmv3.HttpConnectionManager_UpgradeConfig, error) {
	upgradeConfigs := []*hcmv3.HttpConnectionManager_UpgradeConfig{}
	if vs.Spec.UpgradeConfigs != nil {
		for _, upgradeConfig := range vs.Spec.UpgradeConfigs {
			uc := &hcmv3.HttpConnectionManager_UpgradeConfig{}
			if err := options.Unmarshaler.Unmarshal(upgradeConfig.Raw, uc); err != nil {
				return upgradeConfigs, errors.WrapUKS(err, errors.UnmarshalMessage)
			}
			if err := uc.ValidateAll(); err != nil {
				return upgradeConfigs, errors.WrapUKS(err, errors.CannotValidateCacheResourceMessage)
			}

			upgradeConfigs = append(upgradeConfigs, uc)
		}
	}

	return upgradeConfigs, nil
}
