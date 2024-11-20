package builder

import (
	"fmt"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	tlsv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func makeEnvoySecretFromKubernetesSecret(kubeSecret *corev1.Secret) ([]*tlsv3.Secret, error) {
	switch kubeSecret.Type {
	case corev1.SecretTypeTLS:
		return makeEnvoyTLSSecret(kubeSecret)
	case corev1.SecretTypeOpaque:
		return makeEnvoyOpaqueSecret(kubeSecret)
	default:
		return nil, fmt.Errorf("unsupported secret type %s", kubeSecret.Type)
	}
}

func makeEnvoyTLSSecret(kubeSecret *corev1.Secret) ([]*tlsv3.Secret, error) {
	secrets := make([]*tlsv3.Secret, 0)

	envoySecret := &tlsv3.Secret{
		Name: fmt.Sprintf("%s/%s", kubeSecret.Namespace, kubeSecret.Name),
		Type: &tlsv3.Secret_TlsCertificate{
			TlsCertificate: &tlsv3.TlsCertificate{
				CertificateChain: &corev3.DataSource{
					Specifier: &corev3.DataSource_InlineBytes{
						InlineBytes: kubeSecret.Data[corev1.TLSCertKey],
					},
				},
				PrivateKey: &corev3.DataSource{
					Specifier: &corev3.DataSource_InlineBytes{
						InlineBytes: kubeSecret.Data[corev1.TLSPrivateKeyKey],
					},
				},
			},
		},
	}
	if err := envoySecret.ValidateAll(); err != nil {
		return nil, errors.Wrap(err, "cannot validate Envoy Secret")
	}

	secrets = append(secrets, envoySecret)

	return secrets, nil
}

func makeEnvoyOpaqueSecret(kubeSecret *corev1.Secret) ([]*tlsv3.Secret, error) {
	secrets := make([]*tlsv3.Secret, 0)

	for k, v := range kubeSecret.Data {
		envoySecret := &tlsv3.Secret{
			Name: fmt.Sprintf("%s/%s/%s", kubeSecret.Namespace, kubeSecret.Name, k),
			Type: &tlsv3.Secret_GenericSecret{
				GenericSecret: &tlsv3.GenericSecret{
					Secret: &corev3.DataSource{
						Specifier: &corev3.DataSource_InlineBytes{
							InlineBytes: v,
						},
					},
				},
			},
		}

		if err := envoySecret.ValidateAll(); err != nil {
			return nil, errors.Wrap(err, "cannot validate Envoy Secret")
		}

		secrets = append(secrets, envoySecret)
	}

	return secrets, nil
}
