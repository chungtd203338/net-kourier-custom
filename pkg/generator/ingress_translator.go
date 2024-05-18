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
// abc
package generator

import (
	"context"
	"crypto/tls"
	"fmt"
	"os"
	"strings"
	"time"

	v3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	envoycorev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpoint "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	route "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	tlsv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
	envoymatcherv3 "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	"github.com/envoyproxy/go-control-plane/pkg/wellknown"
	"google.golang.org/protobuf/types/known/anypb"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/net-kourier/pkg/bonalib"
	pkgconfig "knative.dev/net-kourier/pkg/config"
	envoy "knative.dev/net-kourier/pkg/envoy/api"
	"knative.dev/net-kourier/pkg/reconciler/ingress/config"
	"knative.dev/net-kourier/pkg/region"
	"knative.dev/networking/pkg/apis/networking/v1alpha1"
	"knative.dev/networking/pkg/certificates"
	netconfig "knative.dev/networking/pkg/config"
	"knative.dev/pkg/kmeta"
	"knative.dev/pkg/logging"
	"knative.dev/pkg/tracker"
)

var _ = bonalib.Baka()
var regions = region.InitRegions()

type translatedIngress struct {
	name       types.NamespacedName
	sniMatches []*envoy.SNIMatch
	clusters   []*v3.Cluster

	externalVirtualHostsTest    [][]*route.VirtualHost
	externalTLSVirtualHostsTest [][]*route.VirtualHost
	internalVirtualHostsTest    [][]*route.VirtualHost
}

type IngressTranslator struct {
	secretGetter    func(ns, name string) (*corev1.Secret, error)
	endpointsGetter func(ns, name string) (*corev1.Endpoints, error)
	serviceGetter   func(ns, name string) (*corev1.Service, error)
	tracker         tracker.Interface
}

func NewIngressTranslator(
	secretGetter func(ns, name string) (*corev1.Secret, error),
	endpointsGetter func(ns, name string) (*corev1.Endpoints, error),
	serviceGetter func(ns, name string) (*corev1.Service, error),
	tracker tracker.Interface) IngressTranslator {
	return IngressTranslator{
		secretGetter:    secretGetter,
		endpointsGetter: endpointsGetter,
		serviceGetter:   serviceGetter,
		tracker:         tracker,
	}
}

func (translator *IngressTranslator) translateIngress(ctx context.Context, ingress *v1alpha1.Ingress, extAuthzEnabled bool) (*translatedIngress, error) {
	logger := logging.FromContext(ctx)
	sniMatches := make([]*envoy.SNIMatch, 0, len(ingress.Spec.TLS)) // bonalog: len(ingress.Spec.TLS) == 0

	for _, ingressTLS := range ingress.Spec.TLS { // bonalog: not loop
		if err := trackSecret(translator.tracker, ingressTLS.SecretNamespace, ingressTLS.SecretName, ingress); err != nil { // bonalog: False
			return nil, err
		}

		secret, err := translator.secretGetter(ingressTLS.SecretNamespace, ingressTLS.SecretName)
		if err != nil { // bonalog: False
			return nil, fmt.Errorf("failed to fetch secret: %w", err)
		}

		// Validate certificate here as these are defined by users.
		// We should not send Gateway without validation.
		_, err = tls.X509KeyPair(
			secret.Data[certFieldInSecret],
			secret.Data[keyFieldInSecret],
		)
		if err != nil { // bonalog: False
			return nil, fmt.Errorf("invalid secret is specified: %w", err)
		}

		secretRef := types.NamespacedName{
			Namespace: ingressTLS.SecretNamespace,
			Name:      ingressTLS.SecretName,
		}
		sniMatches = append(sniMatches, &envoy.SNIMatch{
			Hosts:            ingressTLS.Hosts,
			CertSource:       secretRef,
			CertificateChain: secret.Data[certFieldInSecret],
			PrivateKey:       secret.Data[keyFieldInSecret]})
	}

	internalHostsTest := make([][]*route.VirtualHost, len(regions)+1)
	externalHostsTest := make([][]*route.VirtualHost, len(regions)+1)
	externalTLSHostsTest := make([][]*route.VirtualHost, len(regions)+1)

	for i := 0; i < len(regions); i++ {
		internalHostsTest[i] = make([]*route.VirtualHost, 0, len(ingress.Spec.Rules))
		externalHostsTest[i] = make([]*route.VirtualHost, 0, len(ingress.Spec.Rules))
		externalTLSHostsTest[i] = make([]*route.VirtualHost, 0, len(ingress.Spec.Rules))
	}

	clusters := make([]*v3.Cluster, 0, len(ingress.Spec.Rules)) // bonalog: len(ingress.Spec.Rules) == 2
	// one rule is for in-cluster traffic
	// one rule is for external traffic
	for i, rule := range ingress.Spec.Rules { // bonalog: loop 2 times
		ruleName := fmt.Sprintf("(%s/%s).Rules[%d]", ingress.Namespace, ingress.Name, i)

		routes := make([][]*route.Route, len(regions)+1)
		tlsRoutes := make([]*route.Route, 0, len(rule.HTTP.Paths))

		// bonalog: len(rule.HTTP.Paths) == 2
		for _, httpPath := range rule.HTTP.Paths { // loop 2 times
			// Default the path to "/" if none is passed.
			path := httpPath.Path // path == ""
			if path == "" {       // bonalog: True
				path = "/"
			}

			pathName := fmt.Sprintf("%s.Paths[%s]", ruleName, path)
			// pathName == (default/hello).Rules[0].Paths[/]

			wrs := make([][]*route.WeightedCluster_ClusterWeight, len(regions)+1)

			for _, split := range httpPath.Splits { // loop 1 time
				// bonalib.Log("split", split)
				// The FQN of the service is sufficient here, as clusters towards the
				// same service are supposed to be deduplicated anyway.
				splitName := fmt.Sprintf("%s/%s", split.ServiceNamespace, split.ServiceName)
				// splitName == default/hello-00001

				if err := trackService(translator.tracker, split.ServiceNamespace, split.ServiceName, ingress); err != nil { // bonalog: FALSE
					return nil, err
				}

				service, err := translator.serviceGetter(split.ServiceNamespace, split.ServiceName)
				if apierrors.IsNotFound(err) { // bonalog: FALSE
					logger.Warnf("Service '%s/%s' not yet created", split.ServiceNamespace, split.ServiceName)
					// TODO(markusthoemmes): Find out if we should actually `continue` here.
					return nil, nil
				} else if err != nil { // bonalog: FALSE
					return nil, fmt.Errorf("failed to fetch service '%s/%s': %w", split.ServiceNamespace, split.ServiceName, err)
				}

				// Match the ingress' port with a port on the Service to find the target.
				// Also find out if the target supports HTTP2.
				var (
					externalPort  = int32(80)
					targetPort    = int32(80)
					http2         = false
					httpsPortUsed = false
				)
				for _, port := range service.Spec.Ports { // loop 2 times
					if port.Port == split.ServicePort.IntVal || port.Name == split.ServicePort.StrVal { // bonalog: True
						externalPort = port.Port
						targetPort = port.TargetPort.IntVal
					}
					if port.Name == "http2" || port.Name == "h2c" { // bonalog: false
						http2 = true
					}
					if port.Port == split.ServicePort.IntVal && port.Name == "https" { // bonalog: false
						httpsPortUsed = true
					}
				}
				// port.Port == 80 || 443
				// if port.Port == 80:
				// externalPort == 80
				// targetPort == 8012

				// Disable HTTP2 if the annotation is specified.
				if strings.EqualFold(pkgconfig.GetDisableHTTP2(ingress.Annotations), "true") { // bonalog: false
					http2 = false
				}

				var (
					publicLbEndpoints [][]*endpoint.LbEndpoint
					typ               v3.Cluster_DiscoveryType
				)
				if service.Spec.Type == corev1.ServiceTypeExternalName { // bonalog: false
					// If the service is of type ExternalName, we add a single endpoint.
					typ = v3.Cluster_LOGICAL_DNS
					publicLbEndpoints[0] = []*endpoint.LbEndpoint{
						envoy.NewLBEndpoint(service.Spec.ExternalName, uint32(externalPort)),
					}
				} else { // bonalog: True
					// For all other types, fetch the endpoints object.
					endpoints, err := translator.endpointsGetter(split.ServiceNamespace, split.ServiceName)

					// bonalib.Log("a", endpoints)
					if apierrors.IsNotFound(err) { // bonalog: False kubectl get pod
						logger.Warnf("Endpoints '%s/%s' not yet created", split.ServiceNamespace, split.ServiceName)
						// TODO(markusthoemmes): Find out if we should actually `continue` here.
						return nil, nil
					} else if err != nil { // bonalog: false
						return nil, fmt.Errorf("failed to fetch endpoints '%s/%s': %w", split.ServiceNamespace, split.ServiceName, err)
					}

					typ = v3.Cluster_STATIC
					publicLbEndpoints = lbEndpointsForKubeEndpointsTest(endpoints, targetPort)
				}

				connectTimeout := 5 * time.Second

				var transportSocket *envoycorev3.TransportSocket

				// This has to be "OrDefaults" because this path could be called before the informers are
				// running when booting the controller up and prefilling the config before making it
				// ready.
				//
				// TODO:
				// Drop this configmap check - issues/968
				// We could determine whether system-internal-tls is enabled or disabled via the flag only,
				// but all conformance tests need to be updated to have the port name so we check the configmap as well.
				//
				// TODO: Or fetch configmap before the loop as per https://github.com/knative-sandbox/net-kourier/pull/959#discussion_r1048441513
				cfg := config.FromContextOrDefaults(ctx)

				// As Ingress with RewriteHost points to ExternalService(kourier-internal), we don't enable TLS.
				if (cfg.Network.SystemInternalTLSEnabled() || httpsPortUsed) && httpPath.RewriteHost == "" { // bonalog: false
					var err error
					transportSocket, err = translator.createUpstreamTransportSocket(http2, split.ServiceNamespace)
					if err != nil { // bonalog: false
						return nil, err
					}
				}
				for i := 0; i < len(regions); i++ {
					for key, value := range regions[i] {
						if key == "name" {
							cluster := envoy.NewCluster(splitName+"_"+value, connectTimeout, publicLbEndpoints[i], http2, transportSocket, typ)
							logger.Debugf("adding cluster: %v", cluster)
							clusters = append(clusters, cluster)
							wrs[i] = append(wrs[i], envoy.NewWeightedCluster(splitName+"_"+value, uint32(split.Percent), split.AppendHeaders))
						}
					}
				}
			}

			if len(wrs) != 0 { // True
				// disable ext_authz filter for HTTP01 challenge when the feature is enabled
				if extAuthzEnabled && strings.HasPrefix(path, "/.well-known/acme-challenge/") { // False
					routes[0] = append(routes[0], envoy.NewRouteExtAuthzDisabled(
						pathName, matchHeadersFromHTTPPath(httpPath), path, wrs[0], 0, httpPath.AppendHeaders, httpPath.RewriteHost))
				} else if _, ok := os.LookupEnv("KOURIER_HTTPOPTION_DISABLED"); !ok && ingress.Spec.HTTPOption == v1alpha1.HTTPOptionRedirected && rule.Visibility == v1alpha1.IngressVisibilityExternalIP {
					// Do not create redirect route when KOURIER_HTTPOPTION_DISABLED is set. This option is useful when front end proxy handles the redirection.
					// e.g. Kourier on OpenShift handles HTTPOption by OpenShift Route so KOURIER_HTTPOPTION_DISABLED should be set.
					routes[0] = append(routes[0], envoy.NewRedirectRoute(
						pathName, matchHeadersFromHTTPPath(httpPath), path))
				} else { // bonalog: True
					// routes = append(routes, envoy.NewRoute(
					// 	pathName, matchHeadersFromHTTPPath(httpPath), path, wrs, 0, httpPath.AppendHeaders, httpPath.RewriteHost, ""))
					// routesCloud = append(routesCloud, envoy.NewRoute(
					// 	pathName, matchHeadersFromHTTPPath(httpPath), path, wrsCloud, 0, httpPath.AppendHeaders, httpPath.RewriteHost, "cloud"))
					// routesEdge = append(routesEdge, envoy.NewRoute(
					// 	pathName, matchHeadersFromHTTPPath(httpPath), path, wrsEdge, 0, httpPath.AppendHeaders, httpPath.RewriteHost, "edge"))
					for i := 0; i < len(regions); i++ {
						for key, value := range regions[i] {
							if key == "label" {
								routes[i] = append(routes[i], envoy.NewRoute(
									pathName, matchHeadersFromHTTPPath(httpPath), path, wrs[i], 0, httpPath.AppendHeaders, httpPath.RewriteHost, value))
							}
						}
					}

				}
				if len(ingress.Spec.TLS) != 0 || useHTTPSListenerWithOneCert() {
					tlsRoutes = append(tlsRoutes, envoy.NewRoute(
						pathName, matchHeadersFromHTTPPath(httpPath), path, wrs[0], 0, httpPath.AppendHeaders, httpPath.RewriteHost))
				}
			}
		}

		if len(routes) == 0 { // False
			// Return nothing if there are not routes to generate.
			return nil, nil
		}
		var virtualTLSHost *route.VirtualHost
		virtualHost := make([]*route.VirtualHost, len(regions)+1)

		if extAuthzEnabled { // bonalog: False
			contextExtensions := kmeta.UnionMaps(map[string]string{
				"client":     "kourier",
				"visibility": string(rule.Visibility),
			}, ingress.GetLabels())
			virtualHost[0] = envoy.NewVirtualHostWithExtAuthz(ruleName, contextExtensions, domainsForRule(rule), routes[0])
			if len(tlsRoutes) != 0 {
				virtualTLSHost = envoy.NewVirtualHostWithExtAuthz(ruleName, contextExtensions, domainsForRule(rule), tlsRoutes)
			}
		} else { // bonalog: True
			for i := 0; i < len(regions); i++ {
				virtualHost[i] = envoy.NewVirtualHost(ruleName, domainsForRule(rule), routes[i])
			}
			if len(tlsRoutes) != 0 { // bonalog: False
				virtualTLSHost = envoy.NewVirtualHost(ruleName, domainsForRule(rule), tlsRoutes)
			}
		}

		// internalHosts = append(internalHosts, virtualHost)
		// internalHostsCloud = append(internalHostsCloud, virtualHostCloud)
		// internalHostsEdge = append(internalHostsEdge, virtualHostEdge)

		for i := 0; i < len(regions); i++ {
			internalHostsTest[i] = append(internalHostsTest[i], virtualHost[i])
			if rule.Visibility == v1alpha1.IngressVisibilityExternalIP {
				externalHostsTest[i] = append(externalHostsTest[i], virtualHost[i])
				if virtualTLSHost != nil {
					externalTLSHostsTest[i] = append(externalTLSHostsTest[i], virtualTLSHost)
				}
			}
		}

	}
	_translatedIngress := &translatedIngress{
		name: types.NamespacedName{
			Namespace: ingress.Namespace,
			Name:      ingress.Name,
		},
		sniMatches:                  sniMatches,
		clusters:                    clusters,
		externalVirtualHostsTest:    externalHostsTest,
		externalTLSVirtualHostsTest: externalTLSHostsTest,
		internalVirtualHostsTest:    internalHostsTest,
	}

	return _translatedIngress, nil
}

func (translator *IngressTranslator) createUpstreamTransportSocket(http2 bool, namespace string) (*envoycorev3.TransportSocket, error) {
	caSecret, err := translator.secretGetter(pkgconfig.ServingNamespace(), netconfig.ServingRoutingCertName)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch activator CA secret: %w", err)
	}
	var alpnProtocols string
	if http2 {
		alpnProtocols = "h2"
	}
	tlsAny, err := anypb.New(createUpstreamTLSContext(caSecret.Data[certificates.CaCertName], namespace, alpnProtocols))
	if err != nil {
		return nil, err
	}
	return &envoycorev3.TransportSocket{
		Name: wellknown.TransportSocketTls,
		ConfigType: &envoycorev3.TransportSocket_TypedConfig{
			TypedConfig: tlsAny,
		},
	}, nil
}

func createUpstreamTLSContext(caCertificate []byte, namespace string, alpnProtocols ...string) *tlsv3.UpstreamTlsContext {
	return &tlsv3.UpstreamTlsContext{
		CommonTlsContext: &tlsv3.CommonTlsContext{
			AlpnProtocols: alpnProtocols,
			TlsParams: &tlsv3.TlsParameters{
				TlsMinimumProtocolVersion: tlsv3.TlsParameters_TLSv1_3,
				TlsMaximumProtocolVersion: tlsv3.TlsParameters_TLSv1_3,
			},
			ValidationContextType: &tlsv3.CommonTlsContext_ValidationContext{
				ValidationContext: &tlsv3.CertificateValidationContext{
					TrustedCa: &envoycorev3.DataSource{
						Specifier: &envoycorev3.DataSource_InlineBytes{
							InlineBytes: caCertificate,
						},
					},
					MatchTypedSubjectAltNames: []*tlsv3.SubjectAltNameMatcher{{
						SanType: tlsv3.SubjectAltNameMatcher_DNS,
						Matcher: &envoymatcherv3.StringMatcher{
							MatchPattern: &envoymatcherv3.StringMatcher_Exact{
								// SAN used by Activator
								Exact: certificates.DataPlaneRoutingSAN,
							},
						},
					}, {
						SanType: tlsv3.SubjectAltNameMatcher_DNS,
						Matcher: &envoymatcherv3.StringMatcher{
							MatchPattern: &envoymatcherv3.StringMatcher_Exact{
								// SAN used by Queue-Proxy in target namespace
								Exact: certificates.DataPlaneUserSAN(namespace),
							},
						},
					}},
				},
			},
		},
	}
}

func trackSecret(t tracker.Interface, ns, name string, ingress *v1alpha1.Ingress) error {
	return t.TrackReference(tracker.Reference{
		Kind:       "Secret",
		APIVersion: "v1",
		Namespace:  ns,
		Name:       name,
	}, ingress)
}

func trackService(t tracker.Interface, svcNs, svcName string, ingress *v1alpha1.Ingress) error {
	if err := t.TrackReference(tracker.Reference{
		Kind:       "Service",
		APIVersion: "v1",
		Namespace:  svcNs,
		Name:       svcName,
	}, ingress); err != nil {
		return fmt.Errorf("could not track service reference: %w", err)
	}

	if err := t.TrackReference(tracker.Reference{
		Kind:       "Endpoints",
		APIVersion: "v1",
		Namespace:  svcNs,
		Name:       svcName,
	}, ingress); err != nil {
		return fmt.Errorf("could not track endpoints reference: %w", err)
	}
	return nil
}

// func lbEndpointsForKubeEndpoints(kubeEndpoints *corev1.Endpoints, targetPort int32) []*endpoint.LbEndpoint {
// 	// bonalib.Log(">>>func lbEndpointsForKubeEndpoints", "")
// 	// bonalib.Log("endpoints", kubeEndpoints.Subsets[0])
// 	var readyAddressCount int
// 	for _, subset := range kubeEndpoints.Subsets {
// 		readyAddressCount += len(subset.Addresses)
// 	}

// 	if readyAddressCount == 0 { // bonalog: false
// 		return nil
// 	}

// 	eps := make([]*endpoint.LbEndpoint, 0, readyAddressCount)
// 	for _, subset := range kubeEndpoints.Subsets {
// 		for _, address := range subset.Addresses {
// 			eps = append(eps, envoy.NewLBEndpoint(address.IP, uint32(targetPort)))
// 		}
// 	}
// 	// bonalib.Log("<<<func lbEndpointsForKubeEndpoints", "")
// 	return eps
// }

func lbEndpointsForKubeEndpointsTest(kubeEndpoints *corev1.Endpoints, targetPort int32) [][]*endpoint.LbEndpoint {
	// bonalib.Log(">>>func lbEndpointsForKubeEndpointsTest", "")
	// bonalib.Log("endpoints", kubeEndpoints.Subsets[0])
	var readyAddressCount int
	for _, subset := range kubeEndpoints.Subsets {
		readyAddressCount += len(subset.Addresses)
	}

	if readyAddressCount == 0 { // bonalog: false
		return nil
	}

	eps := make([][]*endpoint.LbEndpoint, len(regions)+1)
	for i := 0; i < len(regions); i++ {
		for key, value := range regions[i] {
			if key == "label" {
				for _, subset := range kubeEndpoints.Subsets {
					for _, address := range subset.Addresses {
						if *address.NodeName == value {
							eps[i] = append(eps[i], envoy.NewLBEndpoint(address.IP, uint32(targetPort)))
						}
					}
				}
			}
		}
	}
	return eps
}

func matchHeadersFromHTTPPath(httpPath v1alpha1.HTTPIngressPath) []*route.HeaderMatcher {
	matchHeaders := make([]*route.HeaderMatcher, 0, len(httpPath.Headers))

	for header, matchType := range httpPath.Headers {
		matchHeader := &route.HeaderMatcher{
			Name: header,
		}
		if matchType.Exact != "" {

			matchHeader.HeaderMatchSpecifier = &route.HeaderMatcher_StringMatch{
				StringMatch: &envoymatcherv3.StringMatcher{
					MatchPattern: &envoymatcherv3.StringMatcher_Exact{
						Exact: matchType.Exact,
					},
				},
			}
		}
		matchHeaders = append(matchHeaders, matchHeader)
	}
	return matchHeaders
}

// domainsForRule returns all domains for the given rule.
//
// For example, external domains returns domains with the following formats:
//   - sub-route_host.namespace.example.com
//   - sub-route_host.namespace.example.com:*
//
// Somehow envoy doesn't match properly gRPC authorities with ports.
// The fix is to include ":*" in the domains.
// This applies both for internal and external domains.
// More info https://github.com/envoyproxy/envoy/issues/886
func domainsForRule(rule v1alpha1.IngressRule) []string {
	domains := make([]string, 0, 2*len(rule.Hosts))
	for _, host := range rule.Hosts {
		domains = append(domains, host, host+":*")
	}
	return domains
}
