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
	"github.com/kaasops/envoy-xds-controller/pkg/utils/k8s"

	"github.com/go-logr/logr"

	v1alpha1 "github.com/kaasops/envoy-xds-controller/api/v1alpha1"
	"github.com/kaasops/envoy-xds-controller/pkg/config"
	"github.com/kaasops/envoy-xds-controller/pkg/errors"

	api_errors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/discovery"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// VirtualServiceReconciler reconciles a Listener object
type VirtualServiceReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	ListenerEventCh chan event.GenericEvent

	Config          *config.Config
	DiscoveryClient *discovery.DiscoveryClient

	log logr.Logger
}

//+kubebuilder:rbac:groups=envoy.kaasops.io,resources=virtualservices,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=envoy.kaasops.io,resources=virtualservices/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=envoy.kaasops.io,resources=virtualservices/finalizers,verbs=update

func (r *VirtualServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	resourceName := k8s.GetResourceName(req.Namespace, req.Name)

	r.log = log.FromContext(ctx).WithValues("Virtual Service CR", resourceName)
	r.log.Info("Reconciling Virtual Service")

	var virtualServiceStatusMessage v1alpha1.Message

	// Get VirtualService CR instance
	virtualServiceCR, err := r.getInstance(ctx, req)
	if err != nil {
		return ctrl.Result{}, errors.Wrap(err, errors.GetFromKubernetesMessage)
	}

	if virtualServiceCR == nil {
		r.log.V(1).Info("Virtual Service CR not found. Reconcile all Listeners, which may use this Virtual Service")
		listenersList := &v1alpha1.ListenerList{}
		if err := r.List(ctx,
			listenersList,
			client.InNamespace(req.Namespace),
		); err != nil {
			return ctrl.Result{}, errors.Wrap(err, errors.GetFromKubernetesMessage)
		}

		for _, listener := range listenersList.Items {
			r.ListenerEventCh <- event.GenericEvent{
				Object: &listener,
			}
		}

		return ctrl.Result{}, nil
	}

	// Try to fill VirtualService from template
	err = virtualServiceCR.FillFromTemplateIfNeeded(ctx, r.Client)
	if err != nil {
		virtualServiceStatusMessage.Add(err.Error())
		if err := virtualServiceCR.SetError(ctx, r.Client, virtualServiceStatusMessage); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Validate VirtualService CR
	err = virtualServiceCR.Validate(ctx, r.Config, r.Client, r.DiscoveryClient)
	if err != nil {
		virtualServiceStatusMessage.Add(err.Error())
		if err := virtualServiceCR.SetError(ctx, r.Client, virtualServiceStatusMessage); err != nil {
			return ctrl.Result{}, nil
		}
	}

	// Get linked Listener
	listener, err := virtualServiceCR.GetLinkedListener(r.Client)
	if err != nil {
		if errors.NeedStatusUpdate(err) {
			virtualServiceStatusMessage.Add(err.Error())
			if err := virtualServiceCR.SetError(ctx, r.Client, virtualServiceStatusMessage); err != nil {
				return ctrl.Result{}, nil
			}
		}
		return ctrl.Result{}, err
	}

	// TODO: Get all synced resources and add to VS status

	// Start Listener Reconcile
	r.ListenerEventCh <- event.GenericEvent{
		Object: &listener,
	}

	return ctrl.Result{}, nil
}

func (r *VirtualServiceReconciler) getInstance(
	ctx context.Context,
	req ctrl.Request,
) (*v1alpha1.VirtualService, error) {
	virtualServiceCR := &v1alpha1.VirtualService{}
	err := r.Get(ctx, req.NamespacedName, virtualServiceCR)
	if err != nil {
		if api_errors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return virtualServiceCR, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *VirtualServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.VirtualService{}).
		Complete(r)
}
