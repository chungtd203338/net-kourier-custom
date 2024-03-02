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
	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"

	// "log"
	// "knative.dev/net-kourier/pkg/bonalib"
)

// headersToAdd generates a list of HeaderValueOption from a map of headers.
func headersToAdd(headers map[string]string) []*core.HeaderValueOption {
	// rand := bonalib.RandNumber()
	// log.Printf("0---start %v envoy.api.headers.headersToAdd", rand)
	if len(headers) == 0 {
		return nil
	}

	res := make([]*core.HeaderValueOption, 0, len(headers)+1)
	for headerName, headerVal := range headers {
		res = append(res, &core.HeaderValueOption{
			Header: &core.HeaderValue{
				Key:   headerName,
				Value: headerVal,
			},
			// In Knative Serving, headers are set instead of appended.
			// Ref: https://github.com/knative/serving/pull/6366
			AppendAction: core.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
		})
	}

	// log.Printf("0---end   %v envoy.api.headers.headersToAdd", rand)

	return res
}
