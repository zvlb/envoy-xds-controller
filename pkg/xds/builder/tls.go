package builder

import (
	"context"
	"fmt"

	"github.com/kaasops/envoy-xds-controller/api/v1alpha1"
	"github.com/kaasops/envoy-xds-controller/pkg/errors"
	"github.com/kaasops/envoy-xds-controller/pkg/options"
	"github.com/kaasops/envoy-xds-controller/pkg/utils"
	"github.com/kaasops/envoy-xds-controller/pkg/utils/k8s"
	"k8s.io/apimachinery/pkg/labels"

	corev1 "k8s.io/api/core/v1"
)

// TODO: reconfigure (don't need Factory here)

var (
	secretLabel = labels.Set{options.SecretLabelKey: options.SdsSecretLabelValue}
)

type TlsFactory struct {
	*v1alpha1.TlsConfig
	vsNamespace       string
	certificatesIndex map[string]corev1.Secret
}

func NewTlsFactory(
	tlsConfig *v1alpha1.TlsConfig,
	namespace string,
	index map[string]corev1.Secret,
) *TlsFactory {
	tf := &TlsFactory{
		TlsConfig:         tlsConfig,
		vsNamespace:       namespace,
		certificatesIndex: index,
	}

	return tf
}

func (tf *TlsFactory) Provide(ctx context.Context, domains []string) (map[string][]string, error) {
	tlsType, err := tf.GetTLSType()
	if err != nil {
		return nil, errors.Wrap(err, "Cannot get TlsConfig type")
	}

	switch tlsType {
	case v1alpha1.SecretRefType:
		return tf.provideSecretRef(domains)
	case v1alpha1.AutoDiscoveryType:
		return tf.provideAutoDiscovery(domains)
	}

	return nil, nil
}

func (tf *TlsFactory) provideSecretRef(domains []string) (map[string][]string, error) {
	var secretNamespace string

	if tf.TlsConfig.SecretRef.Namespace != nil {
		secretNamespace = *tf.TlsConfig.SecretRef.Namespace
	} else {
		secretNamespace = tf.vsNamespace
	}

	return map[string][]string{
		k8s.GetResourceName(secretNamespace, tf.TlsConfig.SecretRef.Name): domains,
	}, nil
}

func (tf *TlsFactory) provideAutoDiscovery(domains []string) (map[string][]string, error) {
	CertificatesWithDomains := make(map[string][]string)

	for _, domain := range domains {
		var secret corev1.Secret
		// Validate certificate exist in index!
		secret, ok := tf.certificatesIndex[domain]
		if !ok {
			wildcardDomain := utils.GetWildcardDomain(domain)
			secret, ok = tf.certificatesIndex[wildcardDomain]
			if !ok {
				return CertificatesWithDomains, errors.NewUKS(fmt.Sprintf("domain - %v: %v", domain, errors.DiscoverNotFoundMessage))
			}
		}

		d, ok := CertificatesWithDomains[k8s.GetResourceName(secret.Namespace, secret.Name)]
		if ok {
			d = append(d, domain)
			CertificatesWithDomains[k8s.GetResourceName(secret.Namespace, secret.Name)] = d
		} else {
			CertificatesWithDomains[k8s.GetResourceName(secret.Namespace, secret.Name)] = []string{domain}
		}
	}

	return CertificatesWithDomains, nil
}
