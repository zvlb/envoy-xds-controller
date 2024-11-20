package k8s

import (
	"context"
	"fmt"
	"strings"

	"github.com/kaasops/envoy-xds-controller/pkg/errors"
	"github.com/kaasops/envoy-xds-controller/pkg/options"
	"golang.org/x/exp/slices"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api_errors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/discovery"
)

func NodeIDs(obj client.Object) []string {
	annotations := obj.GetAnnotations()

	annotation, ok := annotations[options.NodeIDAnnotation]
	if !ok {
		return nil
	}

	nodeIDs := strings.Split(annotation, ",")
	nodeIDs = uniqSlice(nodeIDs)

	// find * in nodeIDs
	for _, nodeID := range nodeIDs {
		if nodeID == "*" {
			return []string{"*"}
		}
	}

	return nodeIDs
}

func uniqSlice(slice []string) []string {
	uniqueMap := make(map[string]struct{}, len(slice))

	for _, s := range slice {
		uniqueMap[s] = struct{}{}
	}

	if len(slice) == len(uniqueMap) {
		return slice
	}

	uniqueSlice := make([]string, 0, len(uniqueMap))

	for s, _ := range uniqueMap {
		uniqueSlice = append(uniqueSlice, s)
	}

	return uniqueSlice
}

func NodeIDsContains(s1, s2 []string) bool {

	if len(s1) > len(s2) {
		return false
	}

	for _, e := range s1 {
		if !slices.Contains(s2, e) {
			return false
		}
	}

	return true
}

func ListSecrets(ctx context.Context, cl client.Client, listOpts client.ListOptions) ([]corev1.Secret, error) {
	secretList := corev1.SecretList{}
	err := cl.List(ctx, &secretList, &listOpts)
	if err != nil {
		return nil, errors.Wrap(err, "cannot list kubernetes secrets")
	}
	return secretList.Items, nil
}

// ResourceExists returns true if the given resource kind exists
// in the given api groupversion
func ResourceExists(dc discovery.DiscoveryInterface, apiGroupVersion, kind string) (bool, error) {
	apiList, err := dc.ServerResourcesForGroupVersion(apiGroupVersion)
	if err != nil {
		if api_errors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}

	for _, r := range apiList.APIResources {
		if r.Kind == kind {
			return true, nil
		}
	}

	return false, nil
}

// indexCertificateSecrets indexed all certificates for cache
func IndexCertificateSecrets(ctx context.Context, cl client.Client, namespaces []string) (map[string]corev1.Secret, error) {
	indexedSecrets := make(map[string]corev1.Secret)
	secrets, err := GetCertificateSecrets(ctx, cl, namespaces)
	if err != nil {
		return nil, err
	}
	for _, secret := range secrets {
		for _, domain := range strings.Split(secret.Annotations[options.DomainAnnotation], ",") {
			_, ok := indexedSecrets[domain]
			if ok {
				// TODO domain already exist in another secret! Need create error case
				continue
			}
			indexedSecrets[domain] = secret
		}
	}
	return indexedSecrets, nil
}

// getCertificateSecrets gets all certificates from Kubernetes secrets
// if namespace not set - find secrets in all namespaces
// TODO: Change "namespace not set" to funcOpts
func GetCertificateSecrets(ctx context.Context, cl client.Client, namespaces []string) ([]corev1.Secret, error) {
	var secrets []corev1.Secret

	requirement, err := labels.NewRequirement(options.AutoDiscoveryLabel, "==", []string{"true"})
	if err != nil {
		return nil, err
	}
	labelSelector := labels.NewSelector().Add(*requirement)
	listOpt := client.ListOptions{
		LabelSelector: labelSelector,
	}

	if namespaces == nil {
		return ListSecrets(ctx, cl, listOpt)
	}

	for _, namespace := range namespaces {
		listOpt.Namespace = namespace

		namespaceSecrets, err := ListSecrets(ctx, cl, listOpt)
		if err != nil {
			return nil, err
		}

		secrets = append(secrets, namespaceSecrets...)
	}
	return secrets, nil
}

// func ResourceName(namespace, name string) string {
// 	return fmt.Sprintf("%s/%s", namespace, name)
// }

func GetResourceName(namespace, name string) string {
	if namespace == "" {
		namespace = "default"
	}
	return fmt.Sprintf("%s/%s", namespace, name)
}

func SplitResourceName(resourceName string) (namespace, name string, err error) {
	splitResourceName := strings.Split(resourceName, "/")

	if len(splitResourceName) < 2 {
		return "", "", fmt.Errorf("invalid resource name %s", resourceName)
	}

	namespace = splitResourceName[0]
	name = splitResourceName[1]

	return
}
