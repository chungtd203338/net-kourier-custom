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
	"errors"
	"os"
	"sync"

	envoyclusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpoint "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	v3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	route "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	httpconnmanagerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	cachetypes "github.com/envoyproxy/go-control-plane/pkg/cache/types"
	cache "github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/envoyproxy/go-control-plane/pkg/resource/v3"
	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	kubeclient "k8s.io/client-go/kubernetes"
	"knative.dev/net-kourier/pkg/bonalib"
	"knative.dev/net-kourier/pkg/config"
	envoy "knative.dev/net-kourier/pkg/envoy/api"
	rconfig "knative.dev/net-kourier/pkg/reconciler/ingress/config"
	"knative.dev/pkg/system"
)

var _ = bonalib.Baka()

const (
	envCertsSecretNamespace = "CERTS_SECRET_NAMESPACE"
	envCertsSecretName      = "CERTS_SECRET_NAME"
	certFieldInSecret       = "tls.crt"
	keyFieldInSecret        = "tls.key"
)

// ErrDomainConflict is an error produces when two ingresses have conflicting domains.
var ErrDomainConflict = errors.New("ingress has a conflicting domain with another ingress")

type Caches struct {
	mu                  sync.Mutex
	translatedIngresses map[types.NamespacedName]*translatedIngress
	clusters            *ClustersCache
	domainsInUse        sets.String
	statusVirtualHost   *route.VirtualHost

	kubeClient kubeclient.Interface
}

func NewCaches(ctx context.Context, kubernetesClient kubeclient.Interface, extAuthz bool) (*Caches, error) {
	c := &Caches{
		translatedIngresses: make(map[types.NamespacedName]*translatedIngress),
		clusters:            newClustersCache(),
		domainsInUse:        sets.NewString(),
		statusVirtualHost:   statusVHost(),
		kubeClient:          kubernetesClient,
	}

	if extAuthz {
		c.clusters.set(config.ExternalAuthz.Cluster, "__extAuthZCluster", "_internal")
	}
	return c, nil
}

func (caches *Caches) UpdateIngress(ctx context.Context, ingressTranslation *translatedIngress) error {
	// we hold a lock for Updating the ingress, to avoid another worker to generate an snapshot just when we have
	// deleted the ingress before adding it.
	caches.mu.Lock()
	defer caches.mu.Unlock()
	caches.deleteTranslatedIngress(ingressTranslation.name.Name, ingressTranslation.name.Namespace)
	return caches.addTranslatedIngress(ingressTranslation)
}

func (caches *Caches) validateIngress(translatedIngress *translatedIngress) error {
	for i := 0; i < len(regions); i++ {
		for _, vhost := range translatedIngress.internalVirtualHostsTest[i] {
			if caches.domainsInUse.HasAny(vhost.Domains...) {
				return ErrDomainConflict
			}
		}
	}

	return nil
}

func (caches *Caches) addTranslatedIngress(translatedIngress *translatedIngress) error {
	if err := caches.validateIngress(translatedIngress); err != nil {
		return err
	}
	for i := 0; i < len(regions); i++ {
		for _, vhost := range translatedIngress.internalVirtualHostsTest[i] {
			caches.domainsInUse.Insert(vhost.Domains...)
		}
	}

	caches.translatedIngresses[translatedIngress.name] = translatedIngress

	for _, cluster := range translatedIngress.clusters {
		caches.clusters.set(cluster, translatedIngress.name.Name, translatedIngress.name.Namespace)
	}

	return nil
}

// SetOnEvicted allows to set a function that will be executed when any key on the cache expires.
func (caches *Caches) SetOnEvicted(f func(types.NamespacedName, interface{})) {
	caches.clusters.clusters.OnEvicted(func(key string, val interface{}) {
		_, name, namespace := explodeKey(key)
		f(types.NamespacedName{
			Namespace: namespace,
			Name:      name,
		}, val)
	})
}

func (caches *Caches) ToEnvoySnapshot(ctx context.Context) (*cache.Snapshot, error) {
	caches.mu.Lock()
	defer caches.mu.Unlock()

	localVHosts := make([][]*route.VirtualHost, len(regions)+1)
	externalVHosts := make([][]*route.VirtualHost, len(regions)+1)
	externalTLSVHosts := make([][]*route.VirtualHost, len(regions)+1)

	for i := 0; i < len(regions); i++ {
		localVHosts[i] = make([]*route.VirtualHost, 0, len(caches.translatedIngresses)+1)
		externalVHosts[i] = make([]*route.VirtualHost, 0, len(caches.translatedIngresses)+1)
		externalTLSVHosts[i] = make([]*route.VirtualHost, 0, len(caches.translatedIngresses)+1)
	}

	snis := sniMatches{}

	for _, translatedIngress := range caches.translatedIngresses {
		for i := 0; i < len(regions); i++ {
			localVHosts[i] = append(localVHosts[i], translatedIngress.internalVirtualHostsTest[i]...)
			externalVHosts[i] = append(externalVHosts[i], translatedIngress.externalVirtualHostsTest[i]...)
			externalTLSVHosts[i] = append(externalTLSVHosts[i], translatedIngress.externalTLSVirtualHostsTest[i]...)
			for _, match := range translatedIngress.sniMatches {
				snis.consume(match)
			}
		}
	}

	// Append the statusHost too.
	for i := 0; i < len(regions); i++ {
		localVHosts[i] = append(localVHosts[i], caches.statusVirtualHost)
	}

	listeners, routes, clusters, err := generateListenersAndRouteConfigsAndClusters(
		ctx,
		externalVHosts,
		externalTLSVHosts,
		localVHosts,
		snis.list(),
		caches.kubeClient,
	)
	if err != nil {
		return nil, err
	}

	clusters = append(caches.clusters.list(), clusters...)

	_snapshot, err := cache.NewSnapshot(
		uuid.NewString(),
		map[resource.Type][]cachetypes.Resource{
			resource.ClusterType:  clusters,
			resource.RouteType:    routes,
			resource.ListenerType: listeners,
		},
	)
	bonalib.Log("snapshot", _snapshot)
	return _snapshot, err
}

// DeleteIngressInfo removes an ingress from the caches.
//
// Notice that the clusters are not deleted. That's handled with the expiration
// time set in the "ClustersCache" struct.
func (caches *Caches) DeleteIngressInfo(ctx context.Context, ingressName string, ingressNamespace string) error {
	caches.mu.Lock()
	defer caches.mu.Unlock()

	caches.deleteTranslatedIngress(ingressName, ingressNamespace)
	return nil
}

func (caches *Caches) deleteTranslatedIngress(ingressName, ingressNamespace string) {
	key := types.NamespacedName{
		Namespace: ingressNamespace,
		Name:      ingressName,
	}

	// Set to expire all the clusters belonging to that Ingress.
	if translated := caches.translatedIngresses[key]; translated != nil {
		for _, cluster := range translated.clusters {
			caches.clusters.setExpiration(cluster.Name, ingressName, ingressNamespace)
		}
		// for i := 0; i < len(regions); i++ {
		// 	for _, vhost := range translated.internalVirtualHostsTest[i] {
		// 		caches.domainsInUse.Delete(vhost.Domains...)
		// 	}
		// }
		for _, vhost := range translated.internalVirtualHostsTest[0] {
			caches.domainsInUse.Delete(vhost.Domains...)
		}

		delete(caches.translatedIngresses, key)
	}
}

func generateListenersAndRouteConfigsAndClusters(
	ctx context.Context,
	externalVirtualHosts [][]*route.VirtualHost,
	externalTLSVirtualHosts [][]*route.VirtualHost,
	clusterLocalVirtualHosts [][]*route.VirtualHost,
	sniMatches []*envoy.SNIMatch,
	kubeclient kubeclient.Interface) ([]cachetypes.Resource, []cachetypes.Resource, []cachetypes.Resource, error) {

	externalRouteConfigName := []string{"external_services_region1", "external_services_region2", "external_services_region3"}
	externalTLSRouteConfigName := []string{"external_tls_services_region1", "external_tls_services_region2", "external_tls_services_region3"}
	internalRouteConfigName := []string{"internal_services_region1", "internal_services_region2", "internal_services_region3"}
	internalTLSRouteConfigName := []string{"internal_tls_services_region1", "internal_tls_services_region2", "internal_tls_services_region3"}
	// This has to be "OrDefaults" because this path is called before the informers are
	// running when booting the controller up and prefilling the config before making it
	// ready.
	cfg := rconfig.FromContextOrDefaults(ctx)

	// First, we save the RouteConfigs with the proper name and all the virtualhosts etc. into the cache.
	externalRouteConfig := make([]*route.RouteConfiguration, len(regions)+1)
	externalTLSRouteConfig := make([]*route.RouteConfiguration, len(regions)+1)
	internalRouteConfig := make([]*route.RouteConfiguration, len(regions)+1)

	externalManager := make([]*httpconnmanagerv3.HttpConnectionManager, len(regions)+1)
	externalTLSManager := make([]*httpconnmanagerv3.HttpConnectionManager, len(regions)+1)
	internalManager := make([]*httpconnmanagerv3.HttpConnectionManager, len(regions)+1)

	for i := 0; i < len(regions); i++ {
		externalRouteConfig[i] = envoy.NewRouteConfig(externalRouteConfigName[i], externalTLSVirtualHosts[i])
		externalTLSRouteConfig[i] = envoy.NewRouteConfig(externalTLSRouteConfigName[i], externalTLSVirtualHosts[i])
		internalRouteConfig[i] = envoy.NewRouteConfig(internalRouteConfigName[i], clusterLocalVirtualHosts[i])
		externalManager[i] = envoy.NewHTTPConnectionManager(externalRouteConfig[i].Name, cfg.Kourier)
		externalTLSManager[i] = envoy.NewHTTPConnectionManager(externalTLSRouteConfig[i].Name, cfg.Kourier)
		internalManager[i] = envoy.NewHTTPConnectionManager(internalRouteConfig[i].Name, cfg.Kourier)
	}

	externalHTTPEnvoyListener, err := envoy.NewHTTPListenerDual(externalManager, config.HTTPPortExternal, cfg.Kourier.EnableProxyProtocol)
	if err != nil { // False
		return nil, nil, nil, err
	}

	internalEnvoyListener, err := envoy.NewHTTPListenerDual(internalManager, config.HTTPPortInternal, false)
	if err != nil { // False
		return nil, nil, nil, err
	}

	listeners := []cachetypes.Resource{externalHTTPEnvoyListener, internalEnvoyListener}
	routes := []cachetypes.Resource{}
	for i := 0; i < len(regions); i++ {
		routes = append(routes, externalRouteConfig[i])
		routes = append(routes, internalRouteConfig[i])
	}

	clusters := make([]cachetypes.Resource, 0, 1)

	// create probe listeners
	probHTTPListener, err := envoy.NewHTTPListenerDual(externalManager, config.HTTPPortProb, false)
	if err != nil { // False
		return nil, nil, nil, err
	}
	listeners = append(listeners, probHTTPListener)

	// Add internal listeners and routes when internal cert secret is specified.
	if cfg.Kourier.ClusterCertSecret != "" { // False
		internalTLSRouteConfig := envoy.NewRouteConfig(internalTLSRouteConfigName[0], clusterLocalVirtualHosts[0])
		internalTLSManager := envoy.NewHTTPConnectionManager(internalTLSRouteConfig.Name, cfg.Kourier)

		internalHTTPSEnvoyListener, err := newInternalEnvoyListenerWithOneCert(
			ctx, internalTLSManager, kubeclient,
			cfg.Kourier,
		)

		if err != nil {
			return nil, nil, nil, err
		}

		listeners = append(listeners, internalHTTPSEnvoyListener)
		routes = append(routes, internalTLSRouteConfig)
	}

	// Configure TLS Listener. If there's at least one ingress that contains the
	// TLS field, that takes precedence. If there is not, TLS will be configured
	// using a single cert for all the services if the creds are given via ENV.
	var externalHTTPSEnvoyListener, probHTTPSListener []*v3.Listener
	var externalHTTPSEnvoyListenerWithOneCertFilterChain []*v3.FilterChain

	if len(sniMatches) > 0 { // False

		for i := 0; i < len(regions); i++ {
			externalHTTPSEnvoyListener[i], err = envoy.NewHTTPSListenerWithSNI(
				externalTLSManager[i], config.HTTPSPortExternal,
				sniMatches, cfg.Kourier,
			)
			if err != nil {
				return nil, nil, nil, err
			}
		}

		probeConfig := cfg.Kourier
		probeConfig.EnableProxyProtocol = false // Disable proxy protocol for prober.

		// create https prob listener with SNI
		for i := 0; i < len(regions); i++ {
			probHTTPSListener[i], err = envoy.NewHTTPSListenerWithSNI(
				externalManager[i], config.HTTPSPortProb,
				sniMatches, probeConfig,
			)
			if err != nil {
				return nil, nil, nil, err
			}
		}

		// if a certificate is configured, add a new filter chain to TLS listener
		if useHTTPSListenerWithOneCert() {

			for i := 0; i < len(regions); i++ {
				externalHTTPSEnvoyListenerWithOneCertFilterChain[i], err = newExternalEnvoyListenerWithOneCertFilterChain(
					ctx, externalTLSManager[i], kubeclient, cfg.Kourier,
				)
				if err != nil {
					return nil, nil, nil, err
				}
				externalHTTPSEnvoyListener[i].FilterChains = append(externalHTTPSEnvoyListener[i].FilterChains,
					externalHTTPSEnvoyListenerWithOneCertFilterChain[i])
				probHTTPSListener[i].FilterChains = append(probHTTPSListener[i].FilterChains,
					externalHTTPSEnvoyListenerWithOneCertFilterChain[i])
			}
		}

		for i := 0; i < len(regions); i++ {
			listeners = append(listeners, externalHTTPSEnvoyListener[i], probHTTPSListener[i])
			routes = append(routes, externalTLSRouteConfig[i])
		}
	} else if useHTTPSListenerWithOneCert() { // False
		for i := 0; i < len(regions); i++ {
			externalHTTPSEnvoyListener[i], err = newExternalEnvoyListenerWithOneCert(
				ctx, externalTLSManager[i], kubeclient,
				cfg.Kourier,
			)
			if err != nil {
				return nil, nil, nil, err
			}

			probHTTPSListener[i], err = envoy.NewHTTPSListener(config.HTTPSPortProb, externalHTTPSEnvoyListener[i].FilterChains, false)
			if err != nil {
				return nil, nil, nil, err
			}

			listeners = append(listeners, externalHTTPSEnvoyListener[i], probHTTPSListener[i])
			routes = append(routes, externalTLSRouteConfig[i])
		}
	}

	if cfg.Kourier.Tracing.Enabled { // False
		jaegerCluster := &envoyclusterv3.Cluster{
			Name:                 "tracing-collector",
			ClusterDiscoveryType: &envoyclusterv3.Cluster_Type{Type: envoyclusterv3.Cluster_STRICT_DNS},
			LoadAssignment: &endpoint.ClusterLoadAssignment{
				ClusterName: "tracing-collector",
				Endpoints: []*endpoint.LocalityLbEndpoints{{
					LbEndpoints: []*endpoint.LbEndpoint{{
						HostIdentifier: &endpoint.LbEndpoint_Endpoint{
							Endpoint: &endpoint.Endpoint{
								Address: &core.Address{
									Address: &core.Address_SocketAddress{
										SocketAddress: &core.SocketAddress{
											Protocol: core.SocketAddress_TCP,
											Address:  cfg.Kourier.Tracing.CollectorHost,
											PortSpecifier: &core.SocketAddress_PortValue{
												PortValue: uint32(cfg.Kourier.Tracing.CollectorPort),
											},
											Ipv4Compat: true,
										},
									},
								},
							},
						},
					}},
				}},
			},
		}

		clusters = append(clusters, jaegerCluster)
	}

	return listeners, routes, clusters, nil
}

// Returns true if we need to modify the HTTPS listener with just one cert
// instead of one per ingress
func useHTTPSListenerWithOneCert() bool {
	return os.Getenv(envCertsSecretNamespace) != "" &&
		os.Getenv(envCertsSecretName) != ""
}

func sslCreds(ctx context.Context, kubeClient kubeclient.Interface, secretNamespace string, secretName string) (certificateChain []byte, privateKey []byte, err error) {
	secret, err := kubeClient.CoreV1().Secrets(secretNamespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return nil, nil, err
	}

	return secret.Data[certFieldInSecret], secret.Data[keyFieldInSecret], nil
}

func newExternalEnvoyListenerWithOneCertFilterChain(ctx context.Context, manager *httpconnmanagerv3.HttpConnectionManager, kubeClient kubeclient.Interface, cfg *config.Kourier) (*v3.FilterChain, error) {
	certificateChain, privateKey, err := sslCreds(
		ctx, kubeClient, os.Getenv(envCertsSecretNamespace), os.Getenv(envCertsSecretName),
	)
	if err != nil {
		return nil, err
	}

	return envoy.CreateFilterChainFromCertificateAndPrivateKey(manager, &envoy.Certificate{
		Certificate:        certificateChain,
		PrivateKey:         privateKey,
		PrivateKeyProvider: privateKeyProvider(cfg.EnableCryptoMB),
		CipherSuites:       cfg.CipherSuites.List(),
	})
}

func newExternalEnvoyListenerWithOneCert(ctx context.Context, manager *httpconnmanagerv3.HttpConnectionManager, kubeClient kubeclient.Interface, cfg *config.Kourier) (*v3.Listener, error) {
	filterChain, err := newExternalEnvoyListenerWithOneCertFilterChain(ctx, manager, kubeClient, cfg)
	if err != nil {
		return nil, err
	}

	return envoy.NewHTTPSListener(config.HTTPSPortExternal, []*v3.FilterChain{filterChain}, cfg.EnableProxyProtocol)
}

func newInternalEnvoyListenerWithOneCert(ctx context.Context, manager *httpconnmanagerv3.HttpConnectionManager, kubeClient kubeclient.Interface, cfg *config.Kourier) (*v3.Listener, error) {
	certificateChain, privateKey, err := sslCreds(ctx, kubeClient, system.Namespace(), cfg.ClusterCertSecret)
	if err != nil {
		return nil, err
	}
	filterChain, err := envoy.CreateFilterChainFromCertificateAndPrivateKey(manager, &envoy.Certificate{
		Certificate:        certificateChain,
		PrivateKey:         privateKey,
		PrivateKeyProvider: privateKeyProvider(cfg.EnableCryptoMB),
		CipherSuites:       cfg.CipherSuites.List(),
	})
	if err != nil {
		return nil, err
	}
	return envoy.NewHTTPSListener(config.HTTPSPortInternal, []*v3.FilterChain{filterChain}, cfg.EnableProxyProtocol)
}

func privateKeyProvider(mbEnabled bool) string {
	if mbEnabled {
		return "cryptomb"
	}
	return ""
}
