/*
Copyright 2020 The Knative Authors

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

package generator

import (
	"context"
	"fmt"

	"knative.dev/networking/pkg/apis/networking/v1alpha1"
	"knative.dev/networking/pkg/ingress"

	"knative.dev/net-kourier/pkg/bonalib"
)

var _ = bonalib.Baka()

// UpdateInfoForIngress translates an Ingress into envoy configuration and updates the
// respective caches.
func UpdateInfoForIngress(ctx context.Context, caches *Caches, ing *v1alpha1.Ingress, translator *IngressTranslator, extAuthzEnabled bool) error {
	// Adds a header with the ingress Hash and a random value header to force the config reload.
	// bonalib.Log("UpdateInfoForIngress", "")
	if _, err := ingress.InsertProbe(ing); err != nil {
		return fmt.Errorf("failed to add knative probe header: %w", err)
	}

	ingressTranslation, err := translator.translateIngress(ctx, ing, extAuthzEnabled)
	// bonalib.Log("ingressTranslation", ingressTranslation.clusters[0].LoadAssignment)
	if err != nil {
		return fmt.Errorf("failed to translate ingress: %w", err)
	}

	if ingressTranslation == nil {
		return nil
	}
	return caches.UpdateIngress(ctx, ingressTranslation) // bonalog: nil
}
