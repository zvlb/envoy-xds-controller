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
	"context"
	"fmt"
	"sort"

	"github.com/kaasops/envoy-xds-controller/pkg/errors"
	"github.com/kaasops/envoy-xds-controller/pkg/options"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (l *Listener) SetError(ctx context.Context, cl client.Client, msg Message) error {
	if !l.validAlredySet() && l.messageAlredySet(msg) {
		return nil
	}

	l.Status.Message = msg
	l.Status.Valid = false

	// TODO: Get all linked VirtualServices and update status to false

	return cl.Status().Update(ctx, l.DeepCopy())
}

func (l *Listener) SetValid(ctx context.Context, cl client.Client, msg Message) error {
	// If alredy set, return
	if l.validAlredySet() && l.messageAlredySet(msg) {
		return nil
	}

	l.Status.Message = msg
	l.Status.Valid = true

	return cl.Status().Update(ctx, l.DeepCopy())
}

func (l *Listener) validAlredySet() bool {
	return l.Status.Valid
}

func (l *Listener) messageAlredySet(msg Message) bool {
	if l.Status.Message == msg {
		return true
	}

	return false
}

func (l *Listener) GetLinkedVirtualServices(ctx context.Context, cl client.Client) ([]VirtualService, error) {
	// Get VirtualServices with matching listener
	vsList := &VirtualServiceList{}
	if err := cl.List(
		ctx,
		vsList,
		client.InNamespace(l.Namespace),
		client.MatchingFields{options.VirtualServiceListenerNameField: l.Name},
		client.MatchingFields{options.VirtualServiceStatusValidField: "true"},
	); err != nil {
		return nil, errors.Wrap(err, errors.GetFromKubernetesMessage)
	}

	// Get VirtualServiceTemplates with matching listener
	vstList := &VirtualServiceTemplateList{}
	if err := cl.List(
		ctx,
		vstList,
		client.InNamespace(l.Namespace),
		client.MatchingFields{options.VirtualServiceTemplateListenerNameField: l.Name},
	); err != nil {
		return nil, errors.Wrap(err, errors.GetFromKubernetesMessage)
	}

	// Get VirtualServices with matching VistualServicesTemplates
	for _, vst := range vstList.Items {
		vsListTmp := &VirtualServiceList{}
		if err := cl.List(
			ctx,
			vsListTmp,
			client.InNamespace(l.Namespace),
			client.MatchingFields{options.VirtualServiceListenerNameField: vst.Name},
			client.MatchingFields{options.VirtualServiceStatusValidField: "true"},
		); err != nil {
			return nil, errors.Wrap(err, errors.GetFromKubernetesMessage)
		}

		for _, vs := range vsListTmp.Items {
			// If Listener set - then this vs already taken or not needed
			if vs.Spec.Listener != nil {
				continue
			}
			vsList.Items = append(vsList.Items, vs)
		}
	}

	// Fill VirtualServices from templates
	for _, vs := range vsList.Items {
		err := vs.FillFromTemplateIfNeeded(ctx, cl)
		if err != nil {
			// This error is imposible for Virtual Service witb valid status
			return nil, errors.Wrap(err, fmt.Sprintf("cannot fill VirtualService from template. VirtualService: %s", vs.Name))
		}
	}

	// Sort VirtualServices by creation time
	sort.Slice(vsList.Items, func(i, j int) bool {
		return vsList.Items[i].CreationTimestamp.Before(&vsList.Items[j].CreationTimestamp)
	})

	return vsList.Items, nil
}
