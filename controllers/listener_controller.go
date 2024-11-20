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

package controllers

import (
	"context"
	"github.com/kaasops/envoy-xds-controller/pkg/xds/builder"
	"slices"
	"strings"

	"github.com/go-logr/logr"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	tlsv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
	resourcev3 "github.com/envoyproxy/go-control-plane/pkg/resource/v3"

	v1alpha1 "github.com/kaasops/envoy-xds-controller/api/v1alpha1"
	fcb "github.com/kaasops/envoy-xds-controller/pkg/builder"
	"github.com/kaasops/envoy-xds-controller/pkg/config"
	"github.com/kaasops/envoy-xds-controller/pkg/errors"
	"github.com/kaasops/envoy-xds-controller/pkg/options"
	"github.com/kaasops/envoy-xds-controller/pkg/utils/k8s"
	xdscache "github.com/kaasops/envoy-xds-controller/pkg/xds/cache"

	api_errors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/kubernetes/pkg/util/slice"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// ListenerReconciler reconciles a Listener object
type ListenerReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Cache  *xdscache.Cache
	Config *config.Config

	// TODO: update controller Runtime to use event.GenericEvent
	EventChan chan event.GenericEvent

	log logr.Logger
}

//+kubebuilder:rbac:groups=envoy.kaasops.io,resources=listeners,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=envoy.kaasops.io,resources=listeners/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=envoy.kaasops.io,resources=listeners/finalizers,verbs=update

func (r *ListenerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	resourceName := k8s.GetResourceName(req.Namespace, req.Name)

	r.log = log.FromContext(ctx).WithValues("Listener CR", resourceName)
	r.log.Info("Reconciling listener")

	var listenerStatusMessage v1alpha1.Message

	// Get listener CR instance
	listenerCR := &v1alpha1.Listener{}
	err := r.Get(ctx, req.NamespacedName, listenerCR)
	if err != nil {
		// if listener not found, delete him from cache
		if api_errors.IsNotFound(err) {
			r.log.V(1).Info("Listener CR not found. Deleting object from xDS cache")
			nodeIDs := r.Cache.GetNodeIDsForCustomResource(resourcev3.ListenerType, resourceName)
			if len(nodeIDs) > 0 {
				r.log.V(1).Info("Clean resource from xDS cache", "NodeIDs", nodeIDs)
				if err := r.Cache.Delete(nodeIDs, resourcev3.ListenerType, resourceName); err != nil {
					return ctrl.Result{}, errors.Wrap(err, errors.CannotDeleteFromCacheMessage)
				}
			}
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, errors.Wrap(err, errors.GetFromKubernetesMessage)
	}

	// Validate Listener CR
	if err := listenerCR.Validate(ctx); err != nil {
		r.log.Error(err, "Listener CR validation failed")
		listenerStatusMessage.Add(err.Error())
		if err := listenerCR.SetError(ctx, r.Client, listenerStatusMessage); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Get Envoy Listener from listener CR spec
	listener := &listenerv3.Listener{}
	if err := options.Unmarshaler.Unmarshal(listenerCR.Spec.Raw, listener); err != nil {
		r.log.Error(err, "Cannot unmarshal Listener CR spec")
		listenerStatusMessage.Add(errors.UnmarshalMessage)
		if err := listenerCR.SetError(ctx, r.Client, listenerStatusMessage); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Listener FilterChains must be empty
	if listener.FilterChains != nil {
		r.log.V(1).Info("Listener FilterChains must be empty")
		listenerStatusMessage.Add(errors.ListenerFilterChainMustBeEmptyMessage)
		if err := listenerCR.SetError(ctx, r.Client, listenerStatusMessage); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Get VirtualServices with matching listener
	vsList, err := listenerCR.GetLinkedVirtualServices(ctx, r.Client)
	if err != nil {
		r.log.Error(err, "Cannot get VirtualServices with matching listener")
		listenerStatusMessage.Add(err.Error())
		if err := listenerCR.SetError(ctx, r.Client, listenerStatusMessage); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, err
	}

	if len(vsList) == 0 {
		r.log.V(1).Info("Virtual Services not found")
		listenerStatusMessage.Add(errors.VirtualServicesNotFoundMessage)
		if err := listenerCR.SetError(ctx, r.Client, listenerStatusMessage); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Get all NodeIDs
	var nodeIDs []string
	for _, vs := range vsList {
		vsNodeIDs := k8s.NodeIDs(&vs)
		if len(vsNodeIDs) > 0 {
			if nodeIDs[0] != "*" {
				nodeIDs = append(nodeIDs, nodeIDs...)
			}
		}
	}
	if len(nodeIDs) == 0 {
		r.log.V(1).Info("cannot get NodeIDs from VirtualServices for listener")
		return ctrl.Result{}, nil
	}

	// Create HashMap for fast searching of certificates
	secretIndex, err := k8s.IndexCertificateSecrets(ctx, r.Client, r.Config.GetWatchNamespaces())
	if err != nil {
		return ctrl.Result{}, errors.Wrap(err, "cannot generate TLS certificates index from Kubernetes secrets")
	}

	for _, nodeID := range nodeIDs {
		// errs for collect all errors with processing VirtualServices
		var errs []error

		// chains for collect Filter Chains for listener
		chains := make([]*listenerv3.FilterChain, 0)

		// routeConfigs for collect Routes for listener
		routeConfigs := make([]*routev3.RouteConfiguration, 0)

		// clusters for collect Clusters for listener
		clusters := make([]*clusterv3.Cluster, 0)

		// secret for collect secrets for listener
		secrets := make([]*tlsv3.Secret, 0)

		for _, vs := range vsList {
			//vsResourceName := k8s.GetResourceName(vs.Namespace, vs.Name)

			vsNodeIDs := k8s.NodeIDs(&vs)
			if len(vsNodeIDs) == 0 {
				continue
			}
			if vsNodeIDs[0] == "*" || slices.Contains(vsNodeIDs, nodeID) {

				xdsResourceBuilder, err := builder.New(
					ctx,
					r.Client,
					&vs,
					secretIndex,
				)
				if err != nil {
					if errors.NeedStatusUpdate(err) {
						var vsMessage v1alpha1.Message
						vsMessage.Add(err.Error())
						if err := vs.SetError(ctx, r.Client, vsMessage); err != nil {
							errs = append(errs, err)
						}
						continue
					}
					errs = append(errs, err)
					continue
				}

				vsChains, err := xdsResourceBuilder.BuildFilterChains()
				if err != nil {
					errs = append(errs, err)
					continue
				}

				chains = append(chains, vsChains...)
				routeConfigs = append(routeConfigs, xdsResourceBuilder.GetRouteConfiguration())
				clusters = append(clusters, xdsResourceBuilder.GetUsedClusters()...)
				secrets = append(secrets, xdsResourceBuilder.GetUsedSecrets()...)
			}
		}

	}

	// for _, vs := range vsList {
	// 	tlsFactory := tls.NewTlsFactory(
	// 		ctx,
	// 		vs.Spec.TlsConfig,
	// 		r.Client,
	// 		r.Config.GetDefaultIssuer(),
	// 		listenerCR.Namespace,
	// 		secretIndex,
	// 	)

	// 	vsFactory := virtualservice.NewVirtualServiceFactory(
	// 		r.Client,
	// 		&vs,
	// 		listenerCR,
	// 		*tlsFactory,
	// 	)

	// 	virtSvc, err := vsFactory.Create(ctx, resourceName)
	// 	if err != nil {
	// 		if errors.NeedStatusUpdate(err) {
	// 			var vsMessage v1alpha1.Message
	// 			vsMessage.Add(err.Error())
	// 			if err := listenerCR.SetError(ctx, r.Client, vsMessage); err != nil {
	// 				errs = append(errs, err)
	// 			}
	// 			continue
	// 		}
	// 		errs = append(errs, err)
	// 		continue
	// 	}

	// }

	return ctrl.Result{}, nil
}

// 	listener.Name = resourceName

// 	// Save listener FilterChains
// 	listenerFilterChains := listener.FilterChains

// 	// Collect all VirtualServices for listener in each NodeID
// 	// for _, nodeID := range nodeIDs {
// 	// errs for collect all errors with processing VirtualServices
// 	var errs []error

// 	// routeConfigs for collect Routes for listener
// 	routeConfigs := make([]*routev3.RouteConfiguration, 0)

// 	// chains for collect Filter Chains for listener
// 	chains := make([]*listenerv3.FilterChain, 0)

// 	for _, vs := range virtualServices.Items {
// 		var vsMessage v1alpha1.Message

// 		if err := v1alpha1.FillFromTemplateIfNeeded(ctx, r.Client, &vs); err != nil {
// 			if api_errors.IsNotFound(err) || errors.NeedStatusUpdate(err) {
// 				vsMessage.Add(errors.Wrap(err, "cannot fill virtual service from template").Error())
// 				if err := vs.SetError(ctx, r.Client, vsMessage); err != nil {
// 					errs = append(errs, err)
// 				}
// 				continue
// 			}
// 			errs = append(errs, err)
// 			continue
// 		}

// 		// If Virtual Service has nodeID or Virtual Service don't have any nondeID (or all NodeID)
// 		vsNodeIds := k8s.NodeIDs(&vs)

// 		IntersectionNodeIDs := utils.IntersectionStrings(vsNodeIds, nodeIDs)

// 		if vsNodeIds == nil || len(IntersectionNodeIDs) > 0 {
// 			// Create HashMap for fast searching of certificates
// 			index, err := k8s.IndexCertificateSecrets(ctx, r.Client, r.Config.GetWatchNamespaces())
// 			if err != nil {
// 				return ctrl.Result{}, errors.Wrap(err, "cannot generate TLS certificates index from Kubernetes secrets")
// 			}

// 			// Create Factory for TLS
// 			tlsFactory := tls.NewTlsFactory(
// 				ctx,
// 				vs.Spec.TlsConfig,
// 				r.Client,
// 				r.Config.GetDefaultIssuer(),
// 				instance.Namespace,
// 				index,
// 			)

// 			// Create Factory for VirtualService
// 			vsFactory := virtualservice.NewVirtualServiceFactory(
// 				r.Client,
// 				&vs,
// 				instance,
// 				*tlsFactory,
// 			)

// 			// Create VirtualService
// 			virtSvc, err := vsFactory.Create(ctx, getResourceName(vs.Namespace, vs.Name))
// 			if err != nil {
// 				if errors.NeedStatusUpdate(err) {
// 					vsMessage.Add(errors.Wrap(err, "cannot get Virtual Service struct").Error())
// 					if err := vs.SetError(ctx, r.Client, vsMessage); err != nil {
// 						errs = append(errs, err)
// 					}
// 					continue
// 				}
// 				errs = append(errs, err)
// 				continue
// 			}

// 			// Collect routes
// 			routeConfigs = append(routeConfigs, virtSvc.RouteConfig)

// 			// Get and collect Filter Chains
// 			filterChains, err := virtualservice.FilterChains(&virtSvc)
// 			if err != nil {
// 				if errors.NeedStatusUpdate(err) {
// 					vsMessage.Add(errors.Wrap(err, "cannot get Filter Chains").Error())
// 					if err := vs.SetError(ctx, r.Client, vsMessage); err != nil {
// 						errs = append(errs, err)
// 					}
// 					continue
// 				}
// 				errs = append(errs, err)
// 				continue
// 			}

// 			chains = append(chains, filterChains...)

// 			// If for this vs used some secrets (with certificates), add secrets to cache
// 			if virtSvc.CertificatesWithDomains != nil {
// 				for nn := range virtSvc.CertificatesWithDomains {
// 					if err := r.makeEnvoySecret(ctx, nn, nodeIDs); err != nil {
// 						return ctrl.Result{}, err
// 					}
// 				}

// 				// Add information aboute used secrets to VirtualService
// 				i := 0
// 				keys := make([]string, len(virtSvc.CertificatesWithDomains))
// 				for k := range virtSvc.CertificatesWithDomains {
// 					keys[i] = k
// 					i++
// 				}
// 				if err := vs.SetValidWithUsedSecrets(ctx, r.Client, keys, vsMessage); err != nil {
// 					errs = append(errs, err)
// 				}
// 			} else {
// 				if err := vs.SetValid(ctx, r.Client, vsMessage); err != nil {
// 					errs = append(errs, err)
// 				}
// 			}
// 		}

// 		// Check errors
// 		if len(errs) != 0 {
// 			for _, e := range errs {
// 				r.log.Error(e, "FilterChain build errors")
// 			}

// 			// Stop working with this NodeID
// 			continue
// 		}

// 		// Add builded FilterChains to Listener
// 		listener.FilterChains = append(listenerFilterChains, chains...)
// 	}

// 	// Clear Listener, if don't have FilterChains
// 	if len(listener.FilterChains) == 0 {
// 		r.log.WithValues("NodeIDs", nodeIDs).Info("Listener FilterChain is empty, deleting")

// 		instanceMessage.Add(fmt.Sprintf("NodeIDs: %s, %s", nodeIDs, "Listener FilterChain is empty"))
// 		if err := instance.SetValid(ctx, r.Client, instanceMessage); err != nil {
// 			return ctrl.Result{}, err
// 		}

// 		if err := r.Cache.Delete(nodeIDs, resourcev3.ListenerType, resourceName); err != nil {
// 			return ctrl.Result{}, errors.Wrap(err, errors.CannotDeleteFromCacheMessage)
// 		}

// 		return ctrl.Result{}, nil
// 	}

// 	applyListener := proto.Clone(listener).(*listenerv3.Listener)

// 	// Validate Listener
// 	if err := listener.ValidateAll(); err != nil {
// 		instanceMessage.Add(fmt.Sprintf("NodeID: %s, %s", nodeIDs, errors.CannotValidateCacheResourceMessage))
// 		if err := instance.SetError(ctx, r.Client, instanceMessage); err != nil {
// 			return ctrl.Result{}, err
// 		}
// 		return reconcile.Result{}, errors.WrapUKS(err, errors.CannotValidateCacheResourceMessage)
// 	}

// 	// Update listener in xDS cache
// 	r.log.V(1).WithValues("NodeIDs", nodeIDs).Info("Update listener", "name:", listener.Name)
// 	if err := r.Cache.Update(IntersectionNodeIDs, applyListener, resourceName); err != nil {
// 		return ctrl.Result{}, errors.Wrap(err, errors.CannotUpdateCacheMessage)
// 	}

// 	// Update routes in xDS cache
// 	for _, rtConfig := range routeConfigs {
// 		rtName := getResourceName(resourceName, rtConfig.Name)

// 		// Validate RouteConfig
// 		if err := rtConfig.ValidateAll(); err != nil {
// 			return reconcile.Result{}, errors.WrapUKS(err, errors.CannotValidateCacheResourceMessage)
// 		}

// 		r.log.V(1).WithValues("NodeIDs", nodeIDs).Info("Update route", "name:", rtConfig.Name)
// 		if err := r.Cache.Update(nodeIDs, rtConfig, rtName); err != nil { //BLYAT'
// 			return ctrl.Result{}, errors.Wrap(err, errors.CannotUpdateCacheMessage)
// 		}
// 	}

// 	if err := instance.SetValid(ctx, r.Client, instanceMessage); err != nil {
// 		return ctrl.Result{}, err
// 	}

// 	r.log.Info("Listener reconcilation finished")

// 	return ctrl.Result{}, nil
// }

// SetupWithManager sets up the controller with the Manager.
func (r *ListenerReconciler) SetupWithManager(mgr ctrl.Manager) error {

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Listener{}).
		Complete(r)
}

// func (r *ListenerReconciler) makeEnvoySecret(ctx context.Context, nn string, nodeIDs []string) error {
// 	kubeSecret := &corev1.Secret{}
// 	err := r.Get(ctx, getNamespaceNameFromResourceName(nn), kubeSecret)
// 	if err != nil {
// 		return errors.New("Secret with certificate not found")
// 	}

// 	if kubeSecret.Type != corev1.SecretTypeTLS && kubeSecret.Type != corev1.SecretTypeOpaque {
// 		return errors.New("Kuberentes Secret is not a type TLS or Opaque")
// 	}

// 	envoySecrets, err := xdscache.MakeEnvoySecretFromKubernetesSecret(kubeSecret)
// 	if err != nil {
// 		return err
// 	}

// 	secretName := getResourceName(kubeSecret.Namespace, kubeSecret.Name)

// 	for _, envoySecret := range envoySecrets {
// 		if err := r.Cache.Update(nodeIDs, envoySecret, secretName); err != nil { //BLYAT'
// 			return err
// 		}
// 	}

// 	return nil
// }
