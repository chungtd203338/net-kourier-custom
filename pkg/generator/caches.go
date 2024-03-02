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
	"knative.dev/net-kourier/pkg/config"
	envoy "knative.dev/net-kourier/pkg/envoy/api"
	rconfig "knative.dev/net-kourier/pkg/reconciler/ingress/config"
	"knative.dev/pkg/system"
	"knative.dev/net-kourier/pkg/bonalib"
)

var _ = bonalib.Baka()

const (
	envCertsSecretNamespace    = "CERTS_SECRET_NAMESPACE"
	envCertsSecretName         = "CERTS_SECRET_NAME"
	certFieldInSecret          = "tls.crt"
	keyFieldInSecret           = "tls.key"
	// externalRouteConfigName    = "external_services"
	externalRouteConfigNameCloud = "external_services_cloud"
	externalRouteConfigNameEdge = "external_services_edge"
	// externalTLSRouteConfigName = "external_tls_services"
	externalTLSRouteConfigNameCloud = "external_tls_services_cloud"
	externalTLSRouteConfigNameEdge = "external_tls_services_edge"
	// internalRouteConfigName    = "internal_services"
	internalRouteConfigNameCloud = "internal_services_cloud"
	internalRouteConfigNameEdge = "internal_services_edge"
	internalTLSRouteConfigName = "internal_tls_services"
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
	for _, vhost := range translatedIngress.internalVirtualHostsCloud {
		if caches.domainsInUse.HasAny(vhost.Domains...) {
			return ErrDomainConflict
		}
	}

	for _, vhost := range translatedIngress.internalVirtualHostsEdge {
		if caches.domainsInUse.HasAny(vhost.Domains...) {
			return ErrDomainConflict
		}
	}

	return nil
}

func (caches *Caches) addTranslatedIngress(translatedIngress *translatedIngress) error {
	if err := caches.validateIngress(translatedIngress); err != nil {
		return err
	}

	for _, vhost := range translatedIngress.internalVirtualHostsCloud {
		caches.domainsInUse.Insert(vhost.Domains...)
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

	// localVHosts := make([]*route.VirtualHost, 0, len(caches.translatedIngresses)+1)
	localVHostsCloud := make([]*route.VirtualHost, 0, len(caches.translatedIngresses)+1)
	localVHostsEdge := make([]*route.VirtualHost, 0, len(caches.translatedIngresses)+1)
	// externalVHosts := make([]*route.VirtualHost, 0, len(caches.translatedIngresses))
	externalVHostsCloud := make([]*route.VirtualHost, 0, len(caches.translatedIngresses))
	externalVHostsEdge := make([]*route.VirtualHost, 0, len(caches.translatedIngresses))
	// externalTLSVHosts := make([]*route.VirtualHost, 0, len(caches.translatedIngresses))
	externalTLSVHostsCloud := make([]*route.VirtualHost, 0, len(caches.translatedIngresses))
	externalTLSVHostsEdge := make([]*route.VirtualHost, 0, len(caches.translatedIngresses))
	snis := sniMatches{}

	for _, translatedIngress := range caches.translatedIngresses {
		// localVHosts	= append(localVHosts, translatedIngress.internalVirtualHosts...)
		localVHostsCloud = append(localVHostsCloud, translatedIngress.internalVirtualHostsCloud...)
		localVHostsEdge = append(localVHostsEdge, translatedIngress.internalVirtualHostsEdge...)
		// externalVHosts = append(externalVHosts, translatedIngress.externalVirtualHosts...)
		externalVHostsCloud = append(externalVHostsCloud, translatedIngress.externalVirtualHostsCloud...)
		externalVHostsEdge = append(externalVHostsEdge, translatedIngress.externalVirtualHosts...)
		// externalTLSVHosts = append(externalTLSVHosts, translatedIngress.externalTLSVirtualHosts...)
		externalTLSVHostsCloud = append(externalTLSVHostsCloud, translatedIngress.externalTLSVirtualHosts...)
		externalTLSVHostsEdge = append(externalTLSVHostsEdge, translatedIngress.externalTLSVirtualHosts...)

		for _, match := range translatedIngress.sniMatches {
			snis.consume(match)
		}
	}

	// Append the statusHost too.
	// localVHosts = append(localVHosts, caches.statusVirtualHost)
	localVHostsCloud = append(localVHostsCloud, caches.statusVirtualHost)
	localVHostsEdge = append(localVHostsEdge, caches.statusVirtualHost)

	// bonalib.Log("caches.statusVirtualHost", caches.statusVirtualHost)

	listeners, routes, clusters, err := generateListenersAndRouteConfigsAndClusters(
		ctx,
		// externalVHosts,
		externalVHostsCloud,
		externalVHostsEdge,
		// externalTLSVHosts,
		externalTLSVHostsCloud,
		externalTLSVHostsEdge,
		// localVHosts,
		localVHostsCloud,
		localVHostsEdge,
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
	// bonalib.Log("snapshot", _snapshot.Resources[5].Items["listener_8090"])
	bonalib.Log("snapshot", "")
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

		for _, vhost := range translated.internalVirtualHosts {
			caches.domainsInUse.Delete(vhost.Domains...)
		}

		delete(caches.translatedIngresses, key)
	}
}

func generateListenersAndRouteConfigsAndClusters(
	ctx context.Context,
	// externalVirtualHosts []*route.VirtualHost,
	externalVirtualHostsCloud []*route.VirtualHost,
	externalVirtualHostsEdge []*route.VirtualHost,
	// externalTLSVirtualHosts []*route.VirtualHost,
	externalTLSVirtualHostsCloud []*route.VirtualHost,
	externalTLSVirtualHostsEdge []*route.VirtualHost,
	// clusterLocalVirtualHosts []*route.VirtualHost,
	clusterLocalVirtualHostsCloud []*route.VirtualHost,
	clusterLocalVirtualHostsEdge []*route.VirtualHost,
	sniMatches []*envoy.SNIMatch,
	kubeclient kubeclient.Interface) ([]cachetypes.Resource, []cachetypes.Resource, []cachetypes.Resource, error) {

	// This has to be "OrDefaults" because this path is called before the informers are
	// running when booting the controller up and prefilling the config before making it
	// ready.
	cfg := rconfig.FromContextOrDefaults(ctx)

	// First, we save the RouteConfigs with the proper name and all the virtualhosts etc. into the cache.
	// externalRouteConfig := envoy.NewRouteConfig(externalRouteConfigName, externalVirtualHosts)
	externalRouteConfigCloud := envoy.NewRouteConfig(externalRouteConfigNameCloud, externalVirtualHostsCloud)
	externalRouteConfigEdge := envoy.NewRouteConfig(externalRouteConfigNameEdge, externalVirtualHostsEdge)
	// externalTLSRouteConfig := envoy.NewRouteConfig(externalTLSRouteConfigName, externalTLSVirtualHosts)
	externalTLSRouteConfigCloud := envoy.NewRouteConfig(externalTLSRouteConfigNameCloud, externalTLSVirtualHostsCloud)
	externalTLSRouteConfigEdge := envoy.NewRouteConfig(externalTLSRouteConfigNameEdge, externalTLSVirtualHostsEdge)
	// internalRouteConfig := envoy.NewRouteConfig(internalRouteConfigName, clusterLocalVirtualHosts)
	internalRouteConfigCloud := envoy.NewRouteConfig(internalRouteConfigNameCloud, clusterLocalVirtualHostsCloud)
	internalRouteConfigEdge := envoy.NewRouteConfig(internalRouteConfigNameEdge, clusterLocalVirtualHostsEdge)
	// bonalib.Log("internalRouteConfigCloud", internalRouteConfigCloud)

	// Now we setup connection managers, that reference the routeconfigs via RDS.
	// externalManager := envoy.NewHTTPConnectionManager(externalRouteConfig.Name, cfg.Kourier)
	externalManagerCloud := envoy.NewHTTPConnectionManager(externalRouteConfigCloud.Name, cfg.Kourier)
	externalManagerEdge := envoy.NewHTTPConnectionManager(externalRouteConfigEdge.Name, cfg.Kourier)
	// externalTLSManager := envoy.NewHTTPConnectionManager(externalTLSRouteConfig.Name, cfg.Kourier)
	externalTLSManagerCloud := envoy.NewHTTPConnectionManager(externalTLSRouteConfigCloud.Name, cfg.Kourier)
	externalTLSManagerEdge := envoy.NewHTTPConnectionManager(externalTLSRouteConfigEdge.Name, cfg.Kourier)
	// internalManager := envoy.NewHTTPConnectionManager(internalRouteConfig.Name, cfg.Kourier)
	internalManagerCloud := envoy.NewHTTPConnectionManager(internalRouteConfigCloud.Name, cfg.Kourier)
	internalManagerEdge := envoy.NewHTTPConnectionManager(internalRouteConfigEdge.Name, cfg.Kourier)

	// bonalib.Log("internalManager", internalManager)

	// externalHTTPEnvoyListener, err := envoy.NewHTTPListener(externalManager, config.HTTPPortExternal, cfg.Kourier.EnableProxyProtocol)
	externalHTTPEnvoyListener, err := envoy.NewHTTPListenerDual(externalManagerCloud, externalManagerEdge, config.HTTPPortExternal, cfg.Kourier.EnableProxyProtocol)
	if err != nil { // False
		// bonalib.Log("...", internalRouteConfigCloud)
		// bonalib.Log("...", internalRouteConfigEdge)
		// bonalib.Log("...", internalManagerCloud)
		// bonalib.Log("...", internalManagerEdge)
		return nil, nil, nil, err
	}

	// internalEnvoyListener, err := envoy.NewHTTPListener(internalManager, config.HTTPPortInternal, false)
	internalEnvoyListener, err := envoy.NewHTTPListenerDual(internalManagerCloud, internalManagerEdge, config.HTTPPortInternal, false)
	// internalEnvoyListenerCloud, err := envoy.NewHTTPListener(internalManagerCloud, config.HTTPPortInternal, false)
	// internalEnvoyListenerEdge, err := envoy.NewHTTPListener(internalManagerEdge, config.HTTPPortInternal, false)
	// bonalib.Log("internalEnvoyListener", internalEnvoyListener)
	// bonalib.Log("internalManagerCloud", internalManagerCloud.GetRds().RouteConfigName)
	if err != nil { // False
		// bonalib.Log("...", internalEnvoyListener)
		// bonalib.Log("...", internalEnvoyListenerCloud)
		// bonalib.Log("...", internalEnvoyListenerEdge)
		return nil, nil, nil, err
	}

	listeners := []cachetypes.Resource{externalHTTPEnvoyListener, internalEnvoyListener}
	routes := []cachetypes.Resource{externalRouteConfigCloud, externalRouteConfigEdge, internalRouteConfigCloud, internalRouteConfigEdge}
	// listeners := []cachetypes.Resource{externalHTTPEnvoyListener, internalEnvoyListener}
	// routes := []cachetypes.Resource{externalRouteConfig, internalRouteConfig}
	clusters := make([]cachetypes.Resource, 0, 1)

	// create probe listeners
	// probHTTPListener, err := envoy.NewHTTPListener(externalManagerCloud, config.HTTPPortProb, false)
	probHTTPListener, err := envoy.NewHTTPListenerDual(externalManagerCloud, externalManagerEdge, config.HTTPPortProb, false)
	if err != nil { // False
		return nil, nil, nil, err
	}
	listeners = append(listeners, probHTTPListener)

	// Add internal listeners and routes when internal cert secret is specified.
	if cfg.Kourier.ClusterCertSecret != "" { // False
		internalTLSRouteConfig := envoy.NewRouteConfig(internalTLSRouteConfigName, clusterLocalVirtualHostsCloud)
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
	if len(sniMatches) > 0 { // False
		bonalib.Log("aaa", "")
		externalHTTPSEnvoyListenerCloud, err := envoy.NewHTTPSListenerWithSNI(
			externalTLSManagerCloud, config.HTTPSPortExternal,
			sniMatches, cfg.Kourier,
		)
		if err != nil {
			return nil, nil, nil, err
		}

		externalHTTPSEnvoyListenerEdge, err := envoy.NewHTTPSListenerWithSNI(
			externalTLSManagerEdge, config.HTTPSPortExternal,
			sniMatches, cfg.Kourier,
		)
		if err != nil {
			return nil, nil, nil, err
		}

		probeConfig := cfg.Kourier
		probeConfig.EnableProxyProtocol = false // Disable proxy protocol for prober.

		// create https prob listener with SNI
		probHTTPSListenerCloud, err := envoy.NewHTTPSListenerWithSNI(
			externalManagerCloud, config.HTTPSPortProb,
			sniMatches, probeConfig,
		)
		if err != nil {
			return nil, nil, nil, err
		}

		probHTTPSListenerEdge, err := envoy.NewHTTPSListenerWithSNI(
			externalManagerEdge, config.HTTPSPortProb,
			sniMatches, probeConfig,
		)
		if err != nil {
			return nil, nil, nil, err
		}

		// if a certificate is configured, add a new filter chain to TLS listener
		if useHTTPSListenerWithOneCert() {
			externalHTTPSEnvoyListenerWithOneCertFilterChainCloud, err := newExternalEnvoyListenerWithOneCertFilterChain(
				ctx, externalTLSManagerCloud, kubeclient, cfg.Kourier,
			)
			if err != nil {
				return nil, nil, nil, err
			}

			externalHTTPSEnvoyListenerWithOneCertFilterChainEdge, err := newExternalEnvoyListenerWithOneCertFilterChain(
				ctx, externalTLSManagerEdge, kubeclient, cfg.Kourier,
			)
			if err != nil {
				return nil, nil, nil, err
			}

			// externalHTTPSEnvoyListener.FilterChains = append(externalHTTPSEnvoyListener.FilterChains,
			// 	externalHTTPSEnvoyListenerWithOneCertFilterChain)
			externalHTTPSEnvoyListenerCloud.FilterChains = append(externalHTTPSEnvoyListenerCloud.FilterChains,
				externalHTTPSEnvoyListenerWithOneCertFilterChainCloud)
			externalHTTPSEnvoyListenerEdge.FilterChains = append(externalHTTPSEnvoyListenerEdge.FilterChains,
				externalHTTPSEnvoyListenerWithOneCertFilterChainEdge)
			// probHTTPSListener.FilterChains = append(probHTTPSListener.FilterChains,
			// 	externalHTTPSEnvoyListenerWithOneCertFilterChain)
			probHTTPSListenerCloud.FilterChains = append(probHTTPSListenerCloud.FilterChains,
				externalHTTPSEnvoyListenerWithOneCertFilterChainCloud)
			probHTTPSListenerEdge.FilterChains = append(probHTTPSListenerEdge.FilterChains,
				externalHTTPSEnvoyListenerWithOneCertFilterChainEdge)
		}

		// listeners = append(listeners, externalHTTPSEnvoyListener, probHTTPSListener)
		listeners = append(listeners, externalHTTPSEnvoyListenerCloud, probHTTPSListenerCloud)
		listeners = append(listeners, externalHTTPSEnvoyListenerEdge, probHTTPSListenerEdge)
		// routes = append(routes, externalTLSRouteConfig)
		routes = append(routes, externalTLSRouteConfigCloud)
		routes = append(routes, externalTLSRouteConfigEdge)
	} else if useHTTPSListenerWithOneCert() { // False
		externalHTTPSEnvoyListenerCloud, err := newExternalEnvoyListenerWithOneCert(
			ctx, externalTLSManagerCloud, kubeclient,
			cfg.Kourier,
		)
		if err != nil {
			return nil, nil, nil, err
		}

		externalHTTPSEnvoyListenerEdge, err := newExternalEnvoyListenerWithOneCert(
			ctx, externalTLSManagerEdge, kubeclient,
			cfg.Kourier,
		)
		if err != nil {
			return nil, nil, nil, err
		}

		// create https prob listener
		probHTTPSListenerCloud, err := envoy.NewHTTPSListener(config.HTTPSPortProb, externalHTTPSEnvoyListenerCloud.FilterChains, false)
		if err != nil {
			return nil, nil, nil, err
		}

		probHTTPSListenerEdge, err := envoy.NewHTTPSListener(config.HTTPSPortProb, externalHTTPSEnvoyListenerEdge.FilterChains, false)
		if err != nil {
			return nil, nil, nil, err
		}

		// listeners = append(listeners, externalHTTPSEnvoyListener, probHTTPSListener)
		listeners = append(listeners, externalHTTPSEnvoyListenerCloud, probHTTPSListenerCloud)
		listeners = append(listeners, externalHTTPSEnvoyListenerEdge, probHTTPSListenerEdge)
		// routes = append(routes, externalTLSRouteConfig)
		routes = append(routes, externalTLSRouteConfigCloud)
		routes = append(routes, externalTLSRouteConfigEdge)
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
