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

package envoy

import (
	route "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	"google.golang.org/protobuf/types/known/wrapperspb"
	
	// "log"
	// "knative.dev/net-kourier/pkg/bonalib"
)

// NewWeightedCluster creates a new WeightedCluster.
func NewWeightedCluster(name string, trafficPerc uint32, headers map[string]string) *route.WeightedCluster_ClusterWeight {
	// rand := bonalib.RandNumber()
	// log.Printf("0---%v envoy.api.weighted_host.NewWeightedCluster", rand)
	return &route.WeightedCluster_ClusterWeight{
		Name:                name,
		Weight:              wrapperspb.UInt32(trafficPerc),
		RequestHeadersToAdd: headersToAdd(headers),
	}
}
