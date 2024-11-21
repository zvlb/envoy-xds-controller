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
	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	"github.com/kaasops/envoy-xds-controller/pkg/options"
	"github.com/kaasops/envoy-xds-controller/pkg/utils/k8s"
	"github.com/kaasops/envoy-xds-controller/pkg/xds/builder"
	xdscache "github.com/kaasops/envoy-xds-controller/pkg/xds/cache"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"slices"

	"github.com/go-logr/logr"

	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"

	v1alpha1 "github.com/kaasops/envoy-xds-controller/api/v1alpha1"
	"github.com/kaasops/envoy-xds-controller/pkg/config"
	"github.com/kaasops/envoy-xds-controller/pkg/errors"
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
		//if api_errors.IsNotFound(err) {
		//	r.log.V(1).Info("Listener CR not found. Deleting object from xDS cache")
		//	nodeIDs := r.Cache.GetNodeIDsForCustomResource(resourcev3.ListenerType, resourceName)
		//	if len(nodeIDs) > 0 {
		//		r.log.V(1).Info("Clean resource from xDS cache", "NodeIDs", nodeIDs)
		//		if err := r.Cache.Delete(nodeIDs, resourcev3.ListenerType, resourceName); err != nil {
		//			return ctrl.Result{}, errors.Wrap(err, errors.CannotDeleteFromCacheMessage)
		//		}
		//	}
		//	return ctrl.Result{}, nil
		//}
		// return ctrl.Result{}, errors.Wrap(err, errors.GetFromKubernetesMessage)
		return ctrl.Result{}, nil
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

		return ctrl.Result{}, nil
	}

	if len(vsList) == 0 {
		r.log.V(1).Info("Virtual Services not found")
		listenerStatusMessage.Add(errors.VirtualServicesNotFoundMessage)
		if err := listenerCR.SetValid(ctx, r.Client, listenerStatusMessage); err != nil {
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

		var resourcesV3 []types.Resource

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
				listener.FilterChains = append(listener.FilterChains, vsChains...)

				resourcesV3 = append(resourcesV3, listener)
				resourcesV3 = append(resourcesV3, xdsResourceBuilder.GetRouteConfiguration())
				for _, clusterV3 := range xdsResourceBuilder.GetUsedClusters() {
					resourcesV3 = append(resourcesV3, clusterV3)
				}
				for _, secretV3 := range xdsResourceBuilder.GetUsedSecrets() {
					resourcesV3 = append(resourcesV3, secretV3)
				}
			}
		}

		if err := r.Cache.Update(nodeID, resourcesV3, resourceName); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ListenerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Add listener name to index
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &v1alpha1.VirtualService{}, options.VirtualServiceListenerNameField, func(rawObject client.Object) []string {
		virtualService := rawObject.(*v1alpha1.VirtualService)
		// if listener field is empty use default listener name as index
		if virtualService.Spec.Listener == nil {
			return []string{options.DefaultListenerName}
		}
		return []string{virtualService.Spec.Listener.Name}
	}); err != nil {
		return errors.Wrap(err, "cannot add Listener names to Listener Reconcile Index")
	}

	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &v1alpha1.VirtualServiceTemplate{}, options.VirtualServiceTemplateListenerNameField, func(rawObject client.Object) []string {
		vst := rawObject.(*v1alpha1.VirtualServiceTemplate)
		// if listener field is empty use default listener name as index
		if vst.Spec.Listener == nil {
			return []string{}
		}
		return []string{vst.Spec.Listener.Name}
	}); err != nil {
		return errors.Wrap(err, "cannot add Listener names to Listener Reconcile Index")
	}

	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &v1alpha1.VirtualServiceTemplate{}, options.VirtualServiceTemplateAccessLogConfigNameField, func(rawObject client.Object) []string {
		vst := rawObject.(*v1alpha1.VirtualServiceTemplate)
		// if listener field is empty use default listener name as index
		if vst.Spec.Listener == nil {
			return []string{}
		}
		return []string{vst.Spec.AccessLogConfig.Name}
	}); err != nil {
		return errors.Wrap(err, "cannot add Access log config names to Listener Reconcile Index")
	}

	// Add template name to index
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &v1alpha1.VirtualService{}, options.VirtualServiceTemplateNameField, func(rawObject client.Object) []string {
		virtualService := rawObject.(*v1alpha1.VirtualService)
		if virtualService.Spec.Template == nil {
			return []string{}
		}
		return []string{virtualService.Spec.Template.Name}
	}); err != nil {
		return errors.Wrap(err, "cannot add template names to Listener Reconcile Index")
	}

	// Add vs status state to index
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &v1alpha1.VirtualService{}, options.VirtualServiceStatusValidField, func(rawObject client.Object) []string {
		virtualService := rawObject.(*v1alpha1.VirtualService)
		if virtualService.Spec.Template == nil {
			return []string{}
		}
		return []string{virtualService.Spec.Template.Name}
	}); err != nil {
		return errors.Wrap(err, "cannot add template names to Listener Reconcile Index")
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Listener{}).
		WatchesRawSource(&source.Channel{Source: r.EventChan}, &handler.EnqueueRequestForObject{}).
		Complete(r)
}
