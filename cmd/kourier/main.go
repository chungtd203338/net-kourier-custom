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

package main

import (
	"knative.dev/net-kourier/pkg/config"
	"knative.dev/net-kourier/pkg/reconciler/informerfiltering"
	kourierIngressController "knative.dev/net-kourier/pkg/reconciler/ingress"
	"knative.dev/pkg/signals"

	// This defines the shared main for injected controllers.
	"log"

	"knative.dev/pkg/injection/sharedmain"
	// "math/rand"
	// "github.com/davecgh/go-spew/spew"
	// bonalib "knative.dev/net-kourier/pkg/bonalib"
)

func main() {
	log.Printf("0---Konnichiwa")
	// rand.NewSource(1)

	ctx := informerfiltering.GetContextWithFilteringLabelSelector(signals.NewContext())
	ctx = sharedmain.WithHealthProbesDisabled(ctx)
	sharedmain.MainWithContext(ctx, config.ControllerName, kourierIngressController.NewController)
}
