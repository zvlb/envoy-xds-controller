/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"fmt"
	"github.com/kaasops/envoy-xds-controller/pkg/options"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

//func GetClusterRefByxDSName(
//	ctx context.Context,
//	cl client.Client,
//	namespace, name string,
//) (ResourceRef, error) {
//	clusterList := &ClusterList{}
//	if err := cl.List(
//		ctx,
//		clusterList,
//		client.InNamespace(namespace),
//		client.MatchingFields{options.ClusterNameField: name},
//		client.MatchingFields{options.VirtualServiceStatusValidField: "true"},
//	); err != nil {
//		return ResourceRef{}, errors.Wrap(err, errors.GetFromKubernetesMessage)
//	}
//
//	if len(clusterList.Items) == 0 {
//		return ResourceRef{}, errors.New(fmt.Sprintf("cluster %s not found", name))
//	}
//
//	if len(clusterList.Items) > 1 {
//		return ResourceRef{}, errors.New(fmt.Sprintf("multiple clusters found with name %s", name))
//	}
//
//	cluster := clusterList.Items[0]
//
//	return ResourceRef{
//		Name:      cluster.Name,
//		Namespace: &cluster.Namespace,
//	}, nil
//}

func GetClusterByxDSName(
	ctx context.Context,
	cl client.Client,
	namespace, name string,
) (*Cluster, error) {
	clusterList := &ClusterList{}
	if err := cl.List(
		ctx,
		clusterList,
		client.InNamespace(namespace),
		client.MatchingFields{options.ClusterNameField: name},
		client.MatchingFields{options.VirtualServiceStatusValidField: "true"},
	); err != nil {
		return nil, errors.Wrap(err, errors.GetFromKubernetesMessage)
	}

	if len(clusterList.Items) == 0 {
		return nil, errors.New(fmt.Sprintf("cluster %s not found", name))
	}

	if len(clusterList.Items) > 1 {
		return nil, errors.New(fmt.Sprintf("multiple clusters found with name %s", name))
	}

	return &clusterList.Items[0], nil
}
